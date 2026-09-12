package service

import (
	"context"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type EntityServer struct {
	entityv1.UnimplementedEntityServiceServer
	repo *repository.Memory
}

func New() *EntityServer { return &EntityServer{repo: repository.New()} }

func (s *EntityServer) GetSnapshot(ctx context.Context, req *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	id := req.GetEntityId()
	if !repository.ValidID(id) {
		return nil, status.Error(codes.InvalidArgument, "invalid entity_id")
	}
	row, found := s.repo.Get(id)
	return &entityv1.GetSnapshotResponse{EntityId: id, Snapshot: row, Found: found}, nil
}

// PutSnapshot 仅用于教学，最终由正式接入链路替代。
func (s *EntityServer) PutSnapshot(ctx context.Context, req *entityv1.PutSnapshotRequest) (*entityv1.PutSnapshotResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.repo.Put(req.GetEntityId(), req.GetSnapshot()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// 此处只确认已写入当前进程的内存。
	return &entityv1.PutSnapshotResponse{EntityId: req.GetEntityId()}, nil
}
