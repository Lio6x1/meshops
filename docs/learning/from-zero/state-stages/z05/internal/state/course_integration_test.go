package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"os"
	"strings"
	"testing"
	"time"
)

// Each integration test writes unique view keys and topic/group names. It never
// changes activeKey, flushes Redis, or starts/stops another course's services.
func TestRedisAtomicOrderingAndTombstone(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires MESHOPS_TEST_REDIS_ADDR")
	}
	cache := redis.NewClient(&redis.Options{Addr: addr})
	defer cache.Close()
	r, event := ingestFixture(t)
	e := &Entity{redis: cache, registry: r}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	generation := newID()
	key := viewKey(generation, "t", "p")
	defer cache.Del(context.Background(), key)
	event.EntityVersion = 2
	check := func(v *commonv1.EntityStateEvent, want int) {
		t.Helper()
		n, err := e.projectInto(ctx, generation, v, false)
		if err != nil || n != want {
			t.Fatalf("got %d/%v want %d", n, err, want)
		}
	}
	check(event, 1)
	check(event, 0)
	old := proto.Clone(event).(*commonv1.EntityStateEvent)
	old.EntityVersion = 1
	check(old, -1)
	conflict := proto.Clone(event).(*commonv1.EntityStateEvent)
	conflict.Snapshot.Person.OnDuty = true
	check(conflict, -2)
	deleted := proto.Clone(event).(*commonv1.EntityStateEvent)
	deleted.EntityVersion = 3
	deleted.EventId = "delete"
	deleted.Operation = commonv1.EntityOperation_ENTITY_OPERATION_DELETE
	deleted.Snapshot = nil
	check(deleted, 1)
	check(event, -1)
	got, err := e.snapshot(ctx, generation, "t", "p")
	if err != nil || got.Found {
		t.Fatal(got, err)
	}
	record, err := e.read(ctx, generation, "t", "p")
	if err != nil || record.version != 3 || !record.deleted {
		t.Fatal(record, err)
	}
	if ttl, err := cache.TTL(ctx, key).Result(); err != nil || ttl != -1 {
		t.Fatal("tombstone must not expire", ttl, err)
	}
	other, err := e.snapshot(ctx, generation, "other", "p")
	if err != nil || other.Found {
		t.Fatal("tenant leakage", other, err)
	}
	// A new service instance can read the same durable view without an in-memory map.
	restarted := &Entity{redis: cache, registry: r}
	record, err = restarted.read(ctx, generation, "t", "p")
	if err != nil || record.version != 3 {
		t.Fatal(record, err)
	}
}

func TestKafkaACKThenRedisProjection(t *testing.T) {
	addr, brokers := os.Getenv("MESHOPS_TEST_REDIS_ADDR"), os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" || brokers == "" {
		t.Skip("requires real Redis and Kafka endpoints")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cache := redis.NewClient(&redis.Options{Addr: addr})
	defer cache.Close()
	k := bus.New(strings.Split(brokers, ","))
	defer k.Close()
	prefix := "state-course-" + newID() + "-"
	if err := k.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	r, event := ingestFixture(t)
	e := &Entity{redis: cache, registry: r}
	generation := newID()
	defer cache.Del(context.Background(), viewKey(generation, "t", "p"))
	ingest := NewIngest(platform.Settings{TopicPrefix: prefix}, r, k)
	sourceCtx := platform.WithPrincipal(ctx, platform.Principal{TenantID: "t", Role: "source", SourceID: "s"})
	stream := &reportStream{ctx: sourceCtx, requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "epoch", FirstSequence: 1, Events: []*commonv1.EntityStateEvent{event}}}}
	if err := ingest.ReportEntityStates(stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) != 1 || stream.responses[0].ConfirmedSequence != 1 {
		t.Fatal("no durable ACK", stream.responses)
	}
	absent, err := e.snapshot(ctx, generation, "t", "p")
	if err != nil || absent.Found {
		t.Fatal("projection ran before consumer", absent, err)
	}
	// Start the consumer AFTER the ACK: replay from retained Kafka, not a local callback.
	done := make(chan error, 1)
	go func() {
		done <- k.Consume(ctx, prefix+"projector", prefix+"entity-state-events.v1", func(ctx context.Context, raw []byte) error {
			v, err := e.decode(raw)
			if err != nil {
				return err
			}
			_, err = e.projectInto(ctx, generation, v, false)
			return err
		})
	}()
	defer func() { cancel(); <-done }()
	for {
		got, err := e.snapshot(ctx, generation, "t", "p")
		if err != nil {
			t.Fatal(err)
		}
		if got.Found {
			if got.Version != 1 || got.Snapshot.Power != nil {
				t.Fatal(got)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("projection timeout", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
