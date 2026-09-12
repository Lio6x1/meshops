package state

import (
	"context"
	"example.com/meshops-course/internal/platform"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strconv"
	"time"
)

type entityAddress struct{ tenant, id string }

func (s *subscription) recordSeen(id string, version int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]int64{}
	}
	if s.known == nil {
		s.known = map[string]int64{}
	}
	s.seen[id] = version
	if version > s.known[id] {
		s.known[id] = version
	}
}
func (s *subscription) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure == nil {
		s.failure = err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *subscription) checkVersions(tenant, generation string, versions map[entityAddress]int64, minimum map[string]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	if generation != s.generation {
		return status.Error(codes.FailedPrecondition, "view generation changed; full resync required")
	}
	for id := range s.ids {
		version := versions[entityAddress{tenant, id}]
		expected := s.known[id]
		if pending := s.pending[id]; pending != nil && pending.Version > expected {
			expected = pending.Version
		}
		if version > expected || version < minimum[id] {
			return status.Error(codes.FailedPrecondition, "subscription version gap; full resync required")
		}
	}
	return nil
}

func (e *Entity) registerSubscription(tenant string, sub *subscription) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.subscribers[tenant] == nil {
		e.subscribers[tenant] = map[*subscription]struct{}{}
	}
	e.subscribers[tenant][sub] = struct{}{}
	if e.reconcileCancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		e.reconcileCancel = cancel
		e.reconcileDone = done
		go func() { defer close(done); e.reconcileLoop(ctx) }()
	}
}
func (e *Entity) unregisterSubscription(tenant string, sub *subscription) {
	e.mu.Lock()
	delete(e.subscribers[tenant], sub)
	if len(e.subscribers[tenant]) == 0 {
		delete(e.subscribers, tenant)
	}
	var done chan struct{}
	if len(e.subscribers) == 0 && e.reconcileCancel != nil {
		e.reconcileCancel()
		done = e.reconcileDone
		e.reconcileCancel = nil
		e.reconcileDone = nil
	}
	e.mu.Unlock()
	// A racing new subscriber may start its own sweep. Join only the old worker,
	// outside the mutex it needs to finish; never cancel the replacement worker.
	if done != nil {
		<-done
	}
}
func (e *Entity) reconcileLoop(ctx context.Context) {
	interval := platform.Duration(e.cfg.ReconcileInterval)
	if interval <= 0 {
		interval = 10 * time.Second
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		_ = e.reconcileSubscribers(ctx) // Each affected stream receives the read error.
	}
}

func (e *Entity) readVersions(ctx context.Context, keys map[entityAddress]struct{}) (string, map[entityAddress]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Read the active generation once, then verify it in the metadata pipeline.
	// A concurrent maintenance switch must never validate another namespace.
	current, err := e.generation(ctx)
	if err != nil {
		return "", nil, err
	}
	pipe := e.redis.Pipeline()
	generation := pipe.Get(ctx, e.activeKey())
	commands := make(map[entityAddress]*redis.StringCmd, len(keys))
	for key := range keys {
		commands[key] = pipe.HGet(ctx, viewKey(current, key.tenant, key.id), "version")
	}
	if _, err = pipe.Exec(ctx); err != nil && err != redis.Nil {
		return "", nil, status.Error(codes.Unavailable, "Redis reconciliation failed")
	}
	if generation.Err() != nil || generation.Val() != current {
		return "", nil, status.Error(codes.FailedPrecondition, "view changed during reconciliation")
	}
	versions := make(map[entityAddress]int64, len(keys))
	for key, command := range commands {
		if command.Err() == redis.Nil {
			versions[key] = 0
			continue
		}
		version, err := strconv.ParseInt(command.Val(), 10, 64)
		if command.Err() != nil || err != nil || version < 1 || version > MaxVersion {
			return "", nil, status.Error(codes.Unavailable, "corrupt view version")
		}
		versions[key] = version
	}
	return current, versions, nil
}

func (e *Entity) reconcileSubscribers(ctx context.Context) error {
	type target struct {
		tenant  string
		sub     *subscription
		minimum map[string]int64
	}
	var targets []target
	keys := map[entityAddress]struct{}{}
	e.mu.Lock()
	for tenant, subs := range e.subscribers {
		for sub := range subs {
			sub.mu.Lock()
			if sub.ready {
				minimum := make(map[string]int64, len(sub.seen))
				for id, version := range sub.seen {
					minimum[id] = version
				}
				targets = append(targets, target{tenant, sub, minimum})
				for id := range sub.ids {
					keys[entityAddress{tenant, id}] = struct{}{}
				}
			}
			sub.mu.Unlock()
		}
	}
	e.mu.Unlock()
	if len(targets) == 0 {
		return nil
	}
	generation, versions, err := e.readVersions(ctx, keys)
	for _, target := range targets {
		failure := err
		if failure == nil {
			failure = target.sub.checkVersions(target.tenant, generation, versions, target.minimum)
		}
		if failure != nil {
			target.sub.fail(failure)
		}
	}
	return err
}
