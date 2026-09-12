package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"testing"
	"time"
)

func TestHashIgnoresReceiptButDetectsDomainChange(t *testing.T) {
	_, event := ingestFixture(t)
	first, err := contentHash(event)
	if err != nil {
		t.Fatal(err)
	}
	other := proto.Clone(event).(*commonv1.EntityStateEvent)
	other.ReceivedAt = timestamppb.New(event.ReceivedAt.AsTime().Add(time.Second))
	second, _ := contentHash(other)
	if first != second {
		t.Fatal("receipt changed identity hash")
	}
	other.Snapshot.Person.OnDuty = true
	third, _ := contentHash(other)
	if first == third {
		t.Fatal("domain conflict was invisible")
	}
}
func TestSubscriptionCoalescingAndBounds(t *testing.T) {
	sub := &subscription{ids: map[string]bool{"a": true, "b": true}, pending: map[string]*entityv1.EntityUpdate{}, generation: "g", maxBytes: 1000, maxCount: 1, wake: make(chan struct{}, 1)}
	sub.put(&entityv1.EntityUpdate{EntityId: "a", Version: 2, ViewGeneration: "g", Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE})
	sub.put(&entityv1.EntityUpdate{EntityId: "a", Version: 1, ViewGeneration: "g", Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT})
	updates, err := sub.take()
	if err != nil || len(updates) != 1 || updates[0].Version != 2 || updates[0].Kind != entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE {
		t.Fatal(updates, err)
	}
	sub.put(&entityv1.EntityUpdate{EntityId: "a", Version: 3, ViewGeneration: "g"})
	sub.put(&entityv1.EntityUpdate{EntityId: "b", Version: 1, ViewGeneration: "g"})
	if _, err = sub.take(); status.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	small := &subscription{ids: map[string]bool{"a": true}, pending: map[string]*entityv1.EntityUpdate{}, generation: "g", maxBytes: 1, maxCount: 100, wake: make(chan struct{}, 1)}
	small.put(&entityv1.EntityUpdate{EntityId: "a", Version: 1, ViewGeneration: "g"})
	if _, err = small.take(); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("byte bound", err)
	}
}

// 本测试只使用唯一命名的影子键，不清空 Redis，也不
// 修改 meshops:view:active。请将 MESHOPS_TEST_REDIS_ADDR 设为测试实例。
func TestRedisOrderingAndTombstone(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("integration requires MESHOPS_TEST_REDIS_ADDR; no integration pass claimed")
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
	if result, err := e.projectInto(ctx, generation, event, false); err != nil || result != 1 {
		t.Fatal(result, err)
	}
	old := proto.Clone(event).(*commonv1.EntityStateEvent)
	old.EntityVersion = 1
	if result, err := e.projectInto(ctx, generation, old, false); err != nil || result != -1 {
		t.Fatal(result, err)
	}
	if result, err := e.projectInto(ctx, generation, event, false); err != nil || result != 0 {
		t.Fatal(result, err)
	}
	conflict := proto.Clone(event).(*commonv1.EntityStateEvent)
	conflict.Snapshot.Person.OnDuty = true
	if result, err := e.projectInto(ctx, generation, conflict, false); err != nil || result != -2 {
		t.Fatal(result, err)
	}
	got, err := e.read(ctx, generation, "t", "p")
	if err != nil || got.snapshot.Person.OnDuty {
		t.Fatal(got, err)
	}
	deleted := proto.Clone(event).(*commonv1.EntityStateEvent)
	deleted.EntityVersion = 3
	deleted.EventId = "delete"
	deleted.Operation = commonv1.EntityOperation_ENTITY_OPERATION_DELETE
	deleted.Snapshot = nil
	if _, err = e.projectInto(ctx, generation, deleted, false); err != nil {
		t.Fatal(err)
	}
	if _, err = e.projectInto(ctx, generation, event, false); err != nil {
		t.Fatal(err)
	}
	if ttl, err := cache.TTL(ctx, key).Result(); err != nil || ttl != -1 {
		t.Fatal("tombstone has physical TTL", ttl, err)
	}
}
