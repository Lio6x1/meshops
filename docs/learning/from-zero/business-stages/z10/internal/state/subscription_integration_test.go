package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type snapshotReadGate struct {
	key           string
	armed         atomic.Bool
	read, release chan struct{}
}

func (g *snapshotReadGate) DialHook(next redis.DialHook) redis.DialHook { return next }
func (g *snapshotReadGate) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (g *snapshotReadGate) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "hgetall" && len(cmd.Args()) > 1 && cmd.Args()[1] == g.key && g.armed.CompareAndSwap(true, false) {
			close(g.read)
			select {
			case <-g.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
}

type entityStream struct {
	grpc.ServerStream
	ctx    context.Context
	frames chan *entityv1.EntityUpdate
}

func (s *entityStream) Context() context.Context { return s.ctx }
func (s *entityStream) Send(frame *entityv1.EntityUpdate) error {
	select {
	case s.frames <- proto.Clone(frame).(*entityv1.EntityUpdate):
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func TestRedisSubscriptionInitialRaceDeleteAndGap(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("integration requires MESHOPS_TEST_REDIS_ADDR; no integration pass claimed")
	}
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer cache.Close()
	r, event := ingestFixture(t)
	tenant := "subscribe_test_" + newID()
	source := r.Sources["t:s"]
	source.TenantID = tenant
	binding := r.Bindings["t:p"]
	binding.TenantID = tenant
	r.Sources = map[string]platform.Source{platform.Key(tenant, "s"): source}
	r.Bindings = map[string]platform.Binding{platform.Key(tenant, "p"): binding}
	event.TenantId = tenant
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	generation := newID()
	created, err := cache.SetNX(ctx, activeKey, generation, 0).Result()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		generation, err = cache.Get(ctx, activeKey).Result()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		defer cache.Del(context.Background(), activeKey)
	}
	key := viewKey(generation, tenant, "p")
	defer cache.Del(context.Background(), key)
	e := &Entity{redis: cache, registry: r, cfg: platform.Settings{SubscriberMaxBytes: 8 << 20, SubscriberQueueSize: 1000, ReconcileInterval: "20ms", SlowConsumerTimeout: "1s"}, subscribers: map[string]map[*subscription]struct{}{}}
	if _, err = e.projectInto(ctx, generation, event, false); err != nil {
		t.Fatal(err)
	}
	operatorContext := platform.WithPrincipal(ctx, platform.Principal{TenantID: tenant, Role: "operator"})
	snapshot, err := e.GetSnapshot(operatorContext, &entityv1.GetSnapshotRequest{EntityId: "p"})
	if err != nil || !snapshot.Found || snapshot.Version != 1 || !snapshot.ExpiresAt.AsTime().Before(time.Now()) {
		t.Fatal("stale is still found", snapshot, err)
	}
	otherTenant := platform.WithPrincipal(ctx, platform.Principal{TenantID: "unrelated_tenant", Role: "operator"})
	missing, err := e.GetSnapshot(otherTenant, &entityv1.GetSnapshotRequest{EntityId: "p"})
	if err != nil || missing.Found || missing.Snapshot != nil {
		t.Fatal("cross-tenant visibility", missing, err)
	}
	batch, err := e.BatchGetSnapshots(operatorContext, &entityv1.BatchGetSnapshotsRequest{EntityIds: []string{"missing", "p"}})
	if err != nil || len(batch.Snapshots) != 2 || batch.Snapshots[0].EntityId != "missing" || batch.Snapshots[0].Found || !batch.Snapshots[1].Found {
		t.Fatal("batch ordering", batch, err)
	}
	if _, err = e.BatchGetSnapshots(operatorContext, &entityv1.BatchGetSnapshotsRequest{EntityIds: []string{"p", "p"}}); status.Code(err) != codes.InvalidArgument {
		t.Fatal("duplicate batch accepted", err)
	}
	unavailableCache := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	unavailableCache.Close()
	unavailable := &Entity{redis: unavailableCache, registry: r}
	if _, err = unavailable.GetSnapshot(operatorContext, &entityv1.GetSnapshotRequest{EntityId: "p"}); status.Code(err) != codes.Unavailable {
		t.Fatal("Redis failure presented as absent", err)
	}
	gate := &snapshotReadGate{key: key, read: make(chan struct{}), release: make(chan struct{})}
	gate.armed.Store(true)
	cache.AddHook(gate)
	stream := &entityStream{ctx: platform.WithPrincipal(ctx, platform.Principal{TenantID: tenant, Role: "operator"}), frames: make(chan *entityv1.EntityUpdate, 100)}
	finished := make(chan error, 1)
	go func() {
		finished <- e.Subscribe(&entityv1.SubscribeRequest{EntityIds: []string{"p", "missing"}}, stream)
	}()
	select {
	case <-gate.read:
	case <-ctx.Done():
		t.Fatal("initial read did not begin")
	}
	newer := proto.Clone(event).(*commonv1.EntityStateEvent)
	newer.EntityVersion = 2
	newer.EventId = "v2"
	if _, err = e.projectInto(ctx, generation, newer, true); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	next := func() *entityv1.EntityUpdate {
		select {
		case v := <-stream.frames:
			return v
		case err := <-finished:
			t.Fatalf("stream exited: %v", err)
		case <-ctx.Done():
			t.Fatal("frame timeout")
		}
		return nil
	}
	first := next()
	if first.Kind != entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT || first.Version != 1 {
		t.Fatal(first)
	}
	end := next()
	if end.Kind != entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END {
		t.Fatal(end)
	}
	update := next()
	if update.Kind != entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT || update.Version != 2 || update.SyncId != first.SyncId {
		t.Fatal(update)
	}
	deleted := proto.Clone(newer).(*commonv1.EntityStateEvent)
	deleted.EntityVersion = 3
	deleted.EventId = "v3"
	deleted.Operation = commonv1.EntityOperation_ENTITY_OPERATION_DELETE
	deleted.Snapshot = nil
	if _, err = e.projectInto(ctx, generation, deleted, true); err != nil {
		t.Fatal(err)
	}
	if snapshot, err = e.GetSnapshot(operatorContext, &entityv1.GetSnapshotRequest{EntityId: "p"}); err != nil || snapshot.Found || snapshot.Snapshot != nil {
		t.Fatal("deleted still found", snapshot, err)
	}
	for {
		frame := next()
		if frame.Kind == entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_HEARTBEAT {
			continue
		}
		if frame.Kind != entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE || frame.Version != 3 {
			t.Fatal(frame)
		}
		break
	}
	// Simulate the Redis-commit/process-notification crash window. Reconciliation
	// closes the stream rather than pretending heartbeats guarantee delivery.
	missed := proto.Clone(newer).(*commonv1.EntityStateEvent)
	missed.EntityVersion = 4
	missed.EventId = "v4"
	if _, err = e.projectInto(ctx, generation, missed, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("gap not detected")
	}
	e.mu.Lock()
	subscribers := len(e.subscribers[tenant])
	e.mu.Unlock()
	if subscribers != 0 {
		t.Fatal("subscription not unregistered")
	}
	// A slow stream has an independent sender and cannot delay a healthy stream.
	e.cfg.SlowConsumerTimeout = "30ms"
	slowCtx, slowCancel := context.WithCancel(stream.ctx)
	defer slowCancel()
	slow := &entityStream{ctx: slowCtx, frames: make(chan *entityv1.EntityUpdate)}
	slowDone := make(chan error, 1)
	go func() { slowDone <- e.Subscribe(&entityv1.SubscribeRequest{EntityIds: []string{"p"}}, slow) }()
	healthyCtx, healthyCancel := context.WithCancel(stream.ctx)
	healthy := &entityStream{ctx: healthyCtx, frames: make(chan *entityv1.EntityUpdate, 100)}
	healthyDone := make(chan error, 1)
	go func() { healthyDone <- e.Subscribe(&entityv1.SubscribeRequest{EntityIds: []string{"p"}}, healthy) }()
	for {
		select {
		case frame := <-healthy.frames:
			if frame.Kind == entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END {
				goto initialized
			}
		case err := <-healthyDone:
			t.Fatalf("healthy subscriber exited: %v", err)
		case <-ctx.Done():
			t.Fatal("healthy initialization timeout")
		}
	}
initialized:
	v5 := proto.Clone(missed).(*commonv1.EntityStateEvent)
	v5.EntityVersion = 5
	v5.EventId = "v5"
	if _, err = e.projectInto(ctx, generation, v5, true); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case frame := <-healthy.frames:
			if frame.Kind == entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT && frame.Version == 5 {
				goto delivered
			}
		case err := <-healthyDone:
			t.Fatalf("healthy subscriber stalled: %v", err)
		case <-ctx.Done():
			t.Fatal("healthy delivery timeout")
		}
	}
delivered:
	select {
	case err = <-slowDone:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatal("slow consumer result", err)
		}
	case <-ctx.Done():
		t.Fatal("slow consumer was not disconnected")
	}
	slowCancel() // Mirrors grpc-go cancelling the transport after handler return.
	healthyCancel()
	select {
	case err = <-healthyDone:
		if err != context.Canceled {
			t.Fatal("cancel result", err)
		}
	case <-ctx.Done():
		t.Fatal("cancel did not stop subscription")
	}
	e.mu.Lock()
	subscribers = len(e.subscribers[tenant])
	e.mu.Unlock()
	if subscribers != 0 {
		t.Fatal("slow/cancelled subscription leaked index entry")
	}
}
