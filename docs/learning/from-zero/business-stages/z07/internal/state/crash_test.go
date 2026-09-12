package state

import (
	"context"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/testsupport"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDurableCrashChild(t *testing.T) {
	if os.Getenv("MESHOPS_CRASH_MODE") != "redis_before_offset" {
		return
	}
	ctx := context.Background()
	cache := redis.NewClient(&redis.Options{Addr: os.Getenv("MESHOPS_TEST_REDIS_ADDR"), DB: 15})
	registry, _ := ingestFixture(t)
	e := &Entity{redis: cache, registry: registry, subscribers: map[string]map[*subscription]struct{}{}}
	b := bus.New(strings.Split(os.Getenv("MESHOPS_TEST_KAFKA_BROKERS"), ","))
	prefix := os.Getenv("MESHOPS_CRASH_PREFIX")
	err := b.Consume(ctx, prefix+"projector", prefix+"entity-state-events.v1", func(c context.Context, raw []byte) error {
		// Use a unique shadow namespace to avoid changing a shared active pointer.
		event, err := e.decode(raw)
		if err != nil {
			return err
		}
		if _, err = e.projectInto(c, os.Getenv("MESHOPS_CRASH_GENERATION"), event, false); err != nil {
			return err
		}
		testsupport.Barrier(t)
		return nil
	})
	t.Fatal("missed Redis durable barrier", err)
}

func TestRedisForceKillBeforeOffsetReplaysWithoutRegressing(t *testing.T) {
	addr, brokers := os.Getenv("MESHOPS_TEST_REDIS_ADDR"), os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" || brokers == "" {
		t.Skip("requires real Redis and Kafka")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	generation := newID()
	prefix := "state_crash_" + strings.ReplaceAll(platform.NewID(), "-", "") + "_"
	topic, group := prefix+"entity-state-events.v1", prefix+"projector"
	b := bus.New(strings.Split(brokers, ","))
	defer b.Close()
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		client := &kafka.Client{Addr: kafka.TCP(strings.Split(brokers, ",")...)}
		client.DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}})
	}()
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer cache.Close()
	defer cache.Del(context.Background(), viewKey(generation, "t", "p"))
	registry, event := ingestFixture(t)
	event.EntityVersion = 2
	event.EventId = "version-two"
	raw, _ := proto.Marshal(event)
	if err := b.Publish(ctx, topic, "t:p", raw); err != nil {
		t.Fatal(err)
	}
	event.EntityVersion = 1
	event.EventId = "version-one"
	oldraw, _ := proto.Marshal(event)
	if err := b.Publish(ctx, topic, "t:p", oldraw); err != nil {
		t.Fatal(err)
	}
	testsupport.KillAtBarrier(t, t.TempDir(), "redis_before_offset", "MESHOPS_CRASH_PREFIX="+prefix, "MESHOPS_CRASH_GENERATION="+generation)
	if lag, err := b.Lag(ctx, group, topic); err != nil || lag != 2 {
		t.Fatal("committed before application returned", lag, err)
	}
	e := &Entity{redis: cache, registry: registry}
	before, err := e.read(ctx, generation, "t", "p")
	if err != nil || before.version != 2 {
		t.Fatal("Redis write did not survive kill", err)
	}
	received := make(chan int, 3)
	done := make(chan error, 1)
	cctx, stop := context.WithCancel(ctx)
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
			event, err := e.decode(raw)
			if err != nil {
				return err
			}
			result, err := e.projectInto(c, generation, event, false)
			if err == nil {
				received <- result
			}
			return err
		})
	}()
	for _, want := range []int{0, -1} {
		select {
		case got := <-received:
			if got != want {
				t.Fatal("duplicate/older event changed state", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	after, err := e.read(ctx, generation, "t", "p")
	if err != nil || after.version != 2 || !proto.Equal(before.snapshot, after.snapshot) {
		t.Fatal("recovery regressed durable view", err)
	}
}
