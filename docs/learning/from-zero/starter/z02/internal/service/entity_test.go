package service

import (
 "context"
 "testing"
 entityv1 "example.com/meshops-course/gen/entity/v1"
 "google.golang.org/grpc/codes"
 "google.golang.org/grpc/status"
)

func TestQueryContract(t *testing.T) {
 server := New()
 reply, err := server.GetSnapshot(context.Background(), &entityv1.GetSnapshotRequest{EntityId:"person-001"})
 if err != nil || !reply.GetFound() || reply.GetSnapshot().GetEntityType() != "person" || reply.GetSnapshot().Power != nil { t.Fatalf("person: %v %v", reply, err) }
 reply, err = server.GetSnapshot(context.Background(), &entityv1.GetSnapshotRequest{EntityId:"missing"})
 if err != nil || reply.GetFound() || reply.GetSnapshot() != nil { t.Fatalf("missing: %v %v", reply, err) }
 _, err = server.GetSnapshot(context.Background(), &entityv1.GetSnapshotRequest{})
 if status.Code(err) != codes.InvalidArgument { t.Fatalf("empty: %v", err) }
 ctx, cancel := context.WithCancel(context.Background())
 cancel()
 _, err = server.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId:"person-001"})
 if status.Code(err) != codes.Canceled { t.Fatalf("cancel: %v", err) }
}
