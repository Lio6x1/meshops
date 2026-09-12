package state

import (
	"context"
	"example.com/meshops-course/internal/bus"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedisOOMDoesNotCommitKafkaOffset(t *testing.T) {
	addr, brokers := os.Getenv("MESHOPS_TEST_REDIS_FAULT_ADDR"), os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" || brokers == "" {
		t.Skip("requires dedicated disposable Redis fault instance and Kafka")
	}
	// Fixed fault port is intentionally different from normal reference Redis.
	if addr != "127.0.0.1:16380" {
		t.Fatal("refusing CONFIG SET outside dedicated fault instance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cache := redis.NewClient(&redis.Options{Addr: addr})
	defer cache.Close()
	original, err := cache.ConfigGet(ctx, "maxmemory").Result()
	if err != nil {
		t.Fatal(err)
	}
	defer cache.ConfigSet(context.Background(), "maxmemory", original["maxmemory"])
	r, event := ingestFixture(t)
	if err = cache.SetNX(ctx, activeKey, newID(), 0).Err(); err != nil {
		t.Fatal(err)
	}
	e := &Entity{redis: cache, registry: r, subscribers: map[string]map[*subscription]struct{}{}}
	generation, err := e.generation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Del(context.Background(), viewKey(generation, "t", "p"))
	prefix := "oom_it_" + newID() + "_"
	topic, group := prefix+"entity-state-events.v1", prefix+"projector"
	b := bus.New(strings.Split(brokers, ","))
	defer b.Close()
	if err = b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		client := &kafka.Client{Addr: kafka.TCP(strings.Split(brokers, ",")...)}
		client.DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}})
	}()
	raw, _ := proto.Marshal(event)
	if err = b.Publish(ctx, topic, "t:p", raw); err != nil {
		t.Fatal(err)
	}
	if err = cache.ConfigSet(ctx, "maxmemory", "1").Err(); err != nil {
		t.Fatal(err)
	}
	errorsSeen := make(chan error, 10)
	applied := make(chan struct{}, 1)
	cctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("consumer leaked")
		}
	}()
	go func() {
		done <- b.Consume(cctx, group, topic, func(c context.Context, raw []byte) error {
			err := e.Project(c, raw)
			if err != nil {
				select {
				case errorsSeen <- err:
				default:
				}
			} else {
				select {
				case applied <- struct{}{}:
				default:
				}
			}
			return err
		})
	}()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errorsSeen:
			if !strings.Contains(err.Error(), "OOM") {
				t.Fatal("did not exercise real Redis OOM", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if lag, err := b.Lag(ctx, group, topic); err != nil || lag != 1 {
		t.Fatal("OOM crossed failed offset", lag, err)
	}
	if err = cache.ConfigSet(ctx, "maxmemory", original["maxmemory"]).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-applied:
	case <-ctx.Done():
		t.Fatal("OOM recovery did not replay", ctx.Err())
	}
	v, err := e.read(ctx, generation, "t", "p")
	if err != nil || v.version != 1 {
		t.Fatal("recovery lost event", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		lag, err := b.Lag(ctx, group, topic)
		if err == nil && lag == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery did not commit", lag, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
