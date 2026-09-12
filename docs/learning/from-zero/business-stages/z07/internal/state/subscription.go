package state

import (
	"context"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"sync"
	"time"
)

// One pending slot per entity coalesces only data frames. SNAPSHOT_END and
// HEARTBEAT are sent by the sole stream owner and never enter this map.
type subscription struct {
	mu                        sync.Mutex
	ids                       map[string]bool
	pending                   map[string]*entityv1.EntityUpdate
	bytes, maxBytes, maxCount int
	wake                      chan struct{}
	failure                   error
	generation, syncID        string
}

func (s *subscription) put(update *entityv1.EntityUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil || !s.ids[update.EntityId] {
		return
	}
	if update.ViewGeneration != s.generation {
		s.failure = status.Error(codes.FailedPrecondition, "view changed; reconnect")
	} else {
		old := s.pending[update.EntityId]
		if old != nil && old.Version >= update.Version {
			return
		}
		size := proto.Size(update)
		next := s.bytes + size
		if old != nil {
			next -= proto.Size(old)
		}
		count := len(s.pending)
		if old == nil {
			count++
		}
		if next > s.maxBytes || count > s.maxCount {
			s.failure = status.Error(codes.ResourceExhausted, "subscription buffer full")
		} else {
			s.pending[update.EntityId] = update
			s.bytes = next
		}
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *subscription) take() ([]*entityv1.EntityUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return nil, s.failure
	}
	out := make([]*entityv1.EntityUpdate, 0, len(s.pending))
	for _, v := range s.pending {
		out = append(out, v)
	}
	s.pending = map[string]*entityv1.EntityUpdate{}
	s.bytes = 0
	return out, nil
}
func (e *Entity) notify(tenant, generation string, update *entityv1.EntityUpdate) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for sub := range e.subscribers[tenant] {
		u := proto.Clone(update).(*entityv1.EntityUpdate)
		u.ViewGeneration = generation
		u.SyncId = sub.syncID
		sub.put(u)
	}
}
func (e *Entity) Subscribe(r *entityv1.SubscribeRequest, stream entityv1.EntityService_SubscribeServer) error {
	ctx := stream.Context()
	if err := queryRole(ctx); err != nil {
		return err
	}
	if platform.Identity(ctx).Role == "task_service" {
		return status.Error(codes.PermissionDenied, "subscription role required")
	}
	if r == nil {
		return status.Error(codes.InvalidArgument, "request required")
	}
	//lint:ignore SA1019 Explicitly reject the legacy resume field instead of silently accepting unsupported semantics.
	if len(r.EntityTypes) > 0 || len(r.SourceIds) > 0 || r.Region != "" || r.SnapshotVersion != 0 {
		return status.Error(codes.Unimplemented, "only explicit entity IDs are supported")
	}
	if err := validIDs(r.EntityIds); err != nil {
		return err
	}
	generation, err := e.generation(ctx)
	if err != nil {
		return err
	}
	tenant := platform.Identity(ctx).TenantID
	sub := &subscription{ids: map[string]bool{}, pending: map[string]*entityv1.EntityUpdate{}, maxBytes: e.cfg.SubscriberMaxBytes, maxCount: e.cfg.SubscriberQueueSize, wake: make(chan struct{}, 1), generation: generation, syncID: newID()}
	for _, id := range r.EntityIds {
		sub.ids[id] = true
	}
	// Registration precedes all reads. Notification racing a read is retained and
	// version-filtered after SNAPSHOT_END rather than being lost.
	e.mu.Lock()
	if e.subscribers[tenant] == nil {
		e.subscribers[tenant] = map[*subscription]struct{}{}
	}
	e.subscribers[tenant][sub] = struct{}{}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.subscribers[tenant], sub)
		if len(e.subscribers[tenant]) == 0 {
			delete(e.subscribers, tenant)
		}
		e.mu.Unlock()
	}()
	sendCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan *entityv1.EntityUpdate)
	sentResult := make(chan error, 1)
	// grpc-go cancels a blocked Send when this RPC handler returns. There is one
	// sender goroutine per RPC, never one goroutine per pending frame.
	go func() {
		for {
			select {
			case <-sendCtx.Done():
				return
			case frame := <-frames:
				if frame == nil {
					return
				}
				err := stream.Send(frame)
				select {
				case sentResult <- err:
				case <-sendCtx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}
	}()
	send := func(frame *entityv1.EntityUpdate) error {
		frame.SyncId = sub.syncID
		frame.ViewGeneration = generation
		timer := time.NewTimer(platform.Duration(e.cfg.SlowConsumerTimeout))
		defer timer.Stop()
		select {
		case frames <- frame:
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return status.Error(codes.ResourceExhausted, "slow subscriber")
		}
		select {
		case err := <-sentResult:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return status.Error(codes.ResourceExhausted, "slow subscriber")
		}
	}
	seen := map[string]int64{}
	for _, id := range r.EntityIds {
		v, err := e.read(ctx, generation, tenant, id)
		if err != nil {
			return err
		}
		seen[id] = v.version
		if v.version > 0 && !v.deleted {
			if _, ok := e.registry.Lookup(tenant, id); ok {
				if err = send(v.update(id, entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT)); err != nil {
					return err
				}
			}
		}
	}
	if err = e.reconcile(ctx, tenant, sub, seen); err != nil {
		return err
	}
	if err = send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END}); err != nil {
		return err
	}
	ticker := time.NewTicker(platform.Duration(e.cfg.ReconcileInterval))
	defer ticker.Stop()
	for {
		updates, err := sub.take()
		if err != nil {
			return err
		}
		for _, u := range updates {
			if u.Version > seen[u.EntityId] {
				if err = send(u); err != nil {
					return err
				}
				seen[u.EntityId] = u.Version
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sub.wake:
		case <-ticker.C:
			if err = e.reconcile(ctx, tenant, sub, seen); err != nil {
				return err
			}
			if err = send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_HEARTBEAT}); err != nil {
				return err
			}
		}
	}
}
func (e *Entity) reconcile(ctx context.Context, tenant string, s *subscription, seen map[string]int64) error {
	g, err := e.generation(ctx)
	if err != nil {
		return err
	}
	if g != s.generation {
		return status.Error(codes.FailedPrecondition, "view generation changed; full resync required")
	}
	for id := range s.ids {
		v, err := e.read(ctx, g, tenant, id)
		if err != nil {
			return err
		}
		s.mu.Lock()
		pending := s.pending[id]
		failure := s.failure
		expected := seen[id]
		if pending != nil && pending.Version > expected {
			expected = pending.Version
		}
		s.mu.Unlock()
		if failure != nil {
			return failure
		}
		if v.version > expected || v.version < seen[id] {
			return status.Error(codes.FailedPrecondition, "subscription version gap; full resync required")
		}
	}
	return nil
}
