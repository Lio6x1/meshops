package service

import (
 "context"
 "strings"

 commonv1 "example.com/meshops-course/gen/common/v1"
 entityv1 "example.com/meshops-course/gen/entity/v1"
 "google.golang.org/grpc/codes"
 "google.golang.org/grpc/status"
)

type EntityServer struct {
 entityv1.UnimplementedEntityServiceServer
}

func New() *EntityServer { return &EntityServer{} }

func (s *EntityServer) GetSnapshot(ctx context.Context, req *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
 if err := ctx.Err(); err != nil { return nil, status.FromContextError(err).Err() }
 id := req.GetEntityId()
 if strings.TrimSpace(id) == "" { return nil, status.Error(codes.InvalidArgument, "entity_id is required") }
 // 这条固定记录仅用于观察第一次查询请求的完整往返。
 if id != "person-001" { return &entityv1.GetSnapshotResponse{EntityId:id, Found:false}, nil }
 return &entityv1.GetSnapshotResponse{
  EntityId:id, Found:true,
  Snapshot:&commonv1.EntitySnapshot{
   EntityType:"person",
   Location:&commonv1.Location{Latitude:31.2304, Longitude:121.4737},
  },
 }, nil
}
