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

// PutSnapshot is a teaching-only RPC; final ingestion replaces it.
func (s *EntityServer) PutSnapshot(ctx context.Context, req *entityv1.PutSnapshotRequest) (*entityv1.PutSnapshotResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := s.repo.Put(req.GetEntityId(), req.GetSnapshot()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// This acknowledges an in-process memory write only.
	return &entityv1.PutSnapshotResponse{EntityId: req.GetEntityId()}, nil
}
