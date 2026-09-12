package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"sync"
)

// MemoryEntity 仅用于 Z04 教学投影，此阶段 ACK 对应的数据仍是易失的。
type MemoryEntity struct {
	entityv1.UnimplementedEntityServiceServer
	registry *platform.Registry
	mu       sync.Mutex
	events   map[string]*commonv1.EntityStateEvent
}

func NewMemoryEntity(r *platform.Registry) *MemoryEntity {
	return &MemoryEntity{registry: r, events: map[string]*commonv1.EntityStateEvent{}}
}
func (m *MemoryEntity) Publish(ctx context.Context, topic, key string, raw []byte) error {
	e := new(commonv1.EntityStateEvent)
	if err := proto.Unmarshal(raw, e); err != nil {
		return err
	}
	if err := Validate(e, m.registry); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.events[key]
	if old != nil {
		if old.SourceId != e.SourceId || old.SourceGeneration != e.SourceGeneration {
			return status.Error(codes.FailedPrecondition, "source generation conflict")
		}
		if old.EntityVersion > e.EntityVersion {
			return nil
		}
		if old.EntityVersion == e.EntityVersion {
			a, err := contentHash(old)
			if err != nil {
				return err
			}
			b, err := contentHash(e)
			if err != nil {
				return err
			}
			if old.EventId == e.EventId && a == b {
				return nil
			}
			return status.Error(codes.AlreadyExists, "same version has different content")
		}
	}
	m.events[key] = proto.Clone(e).(*commonv1.EntityStateEvent)
	return nil
}
func (m *MemoryEntity) GetSnapshot(ctx context.Context, r *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	p := platform.Identity(ctx)
	if p.Role != "operator" && p.Role != "admin" {
		return nil, status.Error(codes.PermissionDenied, "query role required")
	}
	if r == nil || !validID(r.EntityId, 128) {
		return nil, status.Error(codes.InvalidArgument, "invalid entity ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := &entityv1.GetSnapshotResponse{EntityId: r.EntityId, ViewGeneration: "z04-memory"}
	e := m.events[platform.Key(p.TenantID, r.EntityId)]
	if e == nil || e.Operation == commonv1.EntityOperation_ENTITY_OPERATION_DELETE {
		return out, nil
	}
	out.Found = true
	out.Version = e.EntityVersion
	out.SourceId = e.SourceId
	out.SourceGeneration = e.SourceGeneration
	out.Snapshot = proto.Clone(e.Snapshot).(*commonv1.EntitySnapshot)
	out.UpdatedAt = e.ReceivedAt
	out.ExpiresAt = e.ExpiresAt
	return out, nil
}
