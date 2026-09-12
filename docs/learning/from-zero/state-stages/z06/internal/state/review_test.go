package state

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metadataCounter struct{ reads atomic.Int64 }

func (c *metadataCounter) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (c *metadataCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (c *metadataCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if cmd.Name() == "hget" {
				c.reads.Add(1)
			}
		}
		return next(ctx, cmds)
	}
}
func TestSharedReconcileReadsUniqueEntityMetadata(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires real Redis")
	}
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 12})
	defer cache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	generation := newID()
	created, err := cache.SetNX(ctx, activeKey, generation, 0).Result()
	if err != nil {
		t.Fatal(err)
	}
	if created {
		defer cache.Del(context.Background(), activeKey)
	} else {
		generation, err = cache.Get(ctx, activeKey).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	tenant := "sweep_" + newID()
	key := viewKey(generation, tenant, "p")
	defer cache.Del(context.Background(), key)
	if err = cache.HSet(ctx, key, "version", 3).Err(); err != nil {
		t.Fatal(err)
	}
	counter := &metadataCounter{}
	cache.AddHook(counter)
	e := &Entity{redis: cache, subscribers: map[string]map[*subscription]struct{}{tenant: {}}}
	for i := 0; i < 20; i++ {
		sub := &subscription{ids: map[string]bool{"p": true}, pending: map[string]*entityv1.EntityUpdate{}, seen: map[string]int64{"p": 3}, known: map[string]int64{"p": 3}, ready: true, generation: generation, wake: make(chan struct{}, 1)}
		e.subscribers[tenant][sub] = struct{}{}
	}
	if err = e.reconcileSubscribers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := counter.reads.Load(); got != 1 {
		t.Fatalf("read %d versions for one shared entity", got)
	}
	if err = cache.HSet(ctx, key, "version", 4).Err(); err != nil {
		t.Fatal(err)
	}
	if err = e.reconcileSubscribers(ctx); err != nil {
		t.Fatal(err)
	}
	for sub := range e.subscribers[tenant] {
		if _, err = sub.take(); err == nil {
			t.Fatal("missed notification not reported to peer")
		}
	}
}

type reviewConsumer struct {
	mu             sync.Mutex
	strictGroup    string
	floors         map[int]int64
	historyStarted chan struct{}
	historyStopped chan struct{}
	failure        error
}

func (b *reviewConsumer) Consume(ctx context.Context, group, topic string, handler func(context.Context, []byte) error) error {
	close(b.historyStarted)
	<-ctx.Done()
	close(b.historyStopped)
	return ctx.Err()
}
func (b *reviewConsumer) ConsumeStrictPartitions(ctx context.Context, group, topic string, floors map[int]int64, handler func(context.Context, []byte) error) error {
	b.mu.Lock()
	b.strictGroup = group
	b.floors = floors
	b.mu.Unlock()
	return b.failure
}
func TestNewEntityAndRunStrictRecoveryLifecycle(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires real Redis")
	}
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 12})
	defer cache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	registry, _ := ingestFixture(t)
	t.Setenv("REVIEW_CURSOR_KEY", strings.Repeat("k", 32))
	cfg := platform.Settings{CursorKeyEnv: "REVIEW_CURSOR_KEY", ConsumerGroupPrefix: "review_", TopicPrefix: "constructor_" + newID() + "_"}
	if _, err := NewEntity(cfg, registry, nil); err == nil {
		t.Fatal("nil Redis accepted")
	}
	e, err := NewEntity(cfg, registry, cache)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := e.generation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Del(context.Background(), e.activeKey())
	group, err := e.ProjectorGroup(ctx)
	if err != nil || !strings.HasSuffix(group, generation) {
		t.Fatalf("projector group not tied to namespace: %q %v", group, err)
	}
	sentinel := errors.New("strict retention failure")
	b := &reviewConsumer{historyStarted: make(chan struct{}), historyStopped: make(chan struct{}), failure: sentinel}
	if err = e.Run(ctx, b); !errors.Is(err, sentinel) {
		t.Fatalf("worker failure lost: %v", err)
	}
	if b.strictGroup != group {
		t.Fatal("wrong strict projector group", b.strictGroup)
	}
	if len(b.floors) != 0 {
		t.Fatal("fresh namespace gained a replay waiver", b.floors)
	}
}

func TestNewRedisNamespaceReplaysCommittedLatestFacts(t *testing.T) {
	addr, brokers := os.Getenv("MESHOPS_TEST_REDIS_ADDR"), os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" || brokers == "" {
		t.Skip("requires real Redis and Kafka")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 12})
	defer cache.Close()
	var err error
	registry, event := ingestFixture(t)
	binding := registry.Bindings["t:p"]
	binding.EntityID = "cold"
	registry.Bindings["t:cold"] = binding
	t.Setenv("REVIEW_CURSOR_KEY", strings.Repeat("k", 32))
	prefix := "namespace_" + strings.ReplaceAll(newID(), "-", "") + "_"
	cfg := platform.Settings{CursorKeyEnv: "REVIEW_CURSOR_KEY", TopicPrefix: prefix, ConsumerGroupPrefix: prefix}
	testActiveKey := (&Entity{cfg: cfg}).activeKey()
	b := bus.New(strings.Split(brokers, ","))
	defer b.Close()
	if err = b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		client := &kafka.Client{Addr: kafka.TCP(strings.Split(brokers, ",")...)}
		result, err := client.DeleteTopics(cleanup, &kafka.DeleteTopicsRequest{Topics: []string{prefix + "entity-state-events.v1", prefix + "task-events.v1", prefix + "task-dlq.v1"}})
		if err != nil {
			t.Error(err)
			return
		}
		for _, err := range result.Errors {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	event.OccurredAt = timestamppb.New(time.Now().Add(-8 * 24 * time.Hour))
	event.ExpiresAt = timestamppb.New(event.OccurredAt.AsTime().Add(30 * time.Second))
	cold := proto.Clone(event).(*commonv1.EntityStateEvent)
	cold.EntityId = "cold"
	cold.EventId = "cold"
	deleted := proto.Clone(event).(*commonv1.EntityStateEvent)
	deleted.Operation = commonv1.EntityOperation_ENTITY_OPERATION_DELETE
	deleted.Snapshot = nil
	deleted.EntityVersion = 2
	deleted.EventId = "deleted"
	for _, v := range []*commonv1.EntityStateEvent{event, cold, deleted} {
		raw, err := proto.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = b.Publish(ctx, prefix+"entity-state-events.v1", platform.Key(v.TenantId, v.EntityId), raw); err != nil {
			t.Fatal(err)
		}
	}
	var generations, groups []string
	removePointer := redis.NewScript(`if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end; return 0`)
	defer func() {
		for _, generation := range generations {
			cache.Del(context.Background(), viewKey(generation, "t", "p"), viewKey(generation, "t", "cold"))
			removePointer.Run(context.Background(), cache, []string{testActiveKey}, generation)
		}
	}()
	for cycle := 0; cycle < 2; cycle++ {
		e, err := NewEntity(cfg, registry, cache)
		if err != nil {
			t.Fatal(err)
		}
		generation, err := e.generation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		generations = append(generations, generation)
		group, err := e.ProjectorGroup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		groups = append(groups, group)
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- e.Run(runCtx, b) }()
		for {
			tombstone, err := e.read(ctx, generation, "t", "p")
			if err != nil {
				stop()
				t.Fatal(err)
			}
			live, err := e.read(ctx, generation, "t", "cold")
			if err != nil {
				stop()
				t.Fatal(err)
			}
			lag, lagErr := b.Lag(ctx, group, prefix+"entity-state-events.v1")
			if tombstone.deleted && tombstone.version == 2 && live.version == 1 && lagErr == nil && lag == 0 {
				break
			}
			select {
			case err := <-done:
				stop()
				t.Fatalf("projector stopped: %v", err)
			case <-ctx.Done():
				stop()
				t.Fatal("lost committed cold facts", ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
		stop()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run leaked workers")
		}
		if cycle == 0 {
			if n, err := removePointer.Run(ctx, cache, []string{testActiveKey}, generation).Int(); err != nil || n != 1 {
				t.Fatal("test pointer removal failed", n, err)
			}
		}
	}
	if groups[0] == groups[1] {
		t.Fatal("lost namespace reused committed group", groups)
	}
}

func TestNotifyFiltersBeforeCloning(t *testing.T) {
	e := &Entity{subscribers: map[string]map[*subscription]struct{}{"t": {}}}
	for i := 0; i < 100; i++ {
		sub := &subscription{ids: map[string]bool{"other": true}}
		e.subscribers["t"][sub] = struct{}{}
	}
	update := &entityv1.EntityUpdate{EntityId: "p", Snapshot: &commonv1.EntitySnapshot{Status: strings.Repeat("x", 4096)}}
	if allocations := testing.AllocsPerRun(20, func() { e.notify("t", "g", update) }); allocations > 2 {
		t.Fatalf("irrelevant fanout cloned payloads: %f allocations", allocations)
	}
}

func TestSharedSweepRegistrationTeardownRace(t *testing.T) {
	e := &Entity{cfg: platform.Settings{ReconcileInterval: "1h"}, subscribers: map[string]map[*subscription]struct{}{}}
	for i := 0; i < 100; i++ {
		first, second := &subscription{}, &subscription{}
		e.registerSubscription("t", first)
		start := make(chan struct{})
		finished := make(chan struct{})
		go func() { <-start; e.unregisterSubscription("t", first); close(finished) }()
		close(start)
		e.registerSubscription("t", second)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("last unsubscribe deadlocked with registration")
		}
		e.mu.Lock()
		running := e.reconcileCancel != nil && len(e.subscribers["t"]) == 1
		e.mu.Unlock()
		if !running {
			t.Fatal("racing registration lost its reconciliation worker")
		}
		e.unregisterSubscription("t", second)
		e.mu.Lock()
		stopped := e.reconcileCancel == nil && len(e.subscribers) == 0
		e.mu.Unlock()
		if !stopped {
			t.Fatal("last subscriber leaked sweep")
		}
	}
}

type transientGenerationRead struct {
	fail   atomic.Bool
	failed chan struct{}
}

func (h *transientGenerationRead) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *transientGenerationRead) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *transientGenerationRead) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" && h.fail.CompareAndSwap(true, false) {
			close(h.failed)
			return errors.New("injected temporary Redis read failure")
		}
		return next(ctx, cmd)
	}
}
func TestGenerationWatcherRecoversDependencyFailure(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires real Redis")
	}
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 12})
	defer cache.Close()
	e := &Entity{redis: cache, cfg: platform.Settings{TopicPrefix: "watch_" + newID() + "_"}}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	generation := newID()
	if err := cache.Set(ctx, e.activeKey(), generation, 0).Err(); err != nil {
		t.Fatal(err)
	}
	defer cache.Del(context.Background(), e.activeKey())
	hook := &transientGenerationRead{failed: make(chan struct{})}
	hook.fail.Store(true)
	cache.AddHook(hook)
	done := make(chan error, 1)
	go func() { done <- e.watchGeneration(ctx, generation) }()
	select {
	case <-hook.failed:
	case <-ctx.Done():
		t.Fatal("watcher did not read generation")
	}
	select {
	case err := <-done:
		t.Fatal("dependency failure terminated worker", err)
	case <-time.After(1100 * time.Millisecond):
	}
	if err := cache.Set(ctx, e.activeKey(), newID(), 0).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "active view changed") {
			t.Fatal("generation switch not terminal", err)
		}
	case <-ctx.Done():
		t.Fatal("generation switch ignored")
	}
}

func TestActiveViewIsScopedToInputTopic(t *testing.T) {
	ordinary := &Entity{}
	first := &Entity{cfg: platform.Settings{TopicPrefix: "first_"}}
	second := &Entity{cfg: platform.Settings{TopicPrefix: "second_"}}
	if ordinary.activeKey() != activeKey || first.activeKey() == second.activeKey() || first.activeKey() == ordinary.activeKey() {
		t.Fatal("topic namespaces share active view")
	}
}
