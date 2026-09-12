package service

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestRPCSemantics(t *testing.T) {
	ctx := context.Background()
	s := New()
	missing, err := s.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: "person-001"})
	if err != nil || missing.GetFound() {
		t.Fatalf("initial: %v %v", missing, err)
	}
	req := &entityv1.PutSnapshotRequest{EntityId: "person-001", Snapshot: &commonv1.EntitySnapshot{EntityType: "person", Location: &commonv1.Location{Latitude: 31, Longitude: 121}}}
	if _, err = s.PutSnapshot(ctx, req); err != nil {
		t.Fatal(err)
	}
	reply, err := s.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: "person-001"})
	if err != nil || !reply.GetFound() || reply.GetSnapshot().GetEntityType() != "person" {
		t.Fatalf("read: %v %v", reply, err)
	}
	req.Snapshot.Location.Latitude = 32
	if _, err = s.PutSnapshot(ctx, req); err != nil {
		t.Fatal(err)
	}
	reply, _ = s.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: "person-001"})
	if reply.GetSnapshot().GetLocation().GetLatitude() != 32 {
		t.Fatal("update not visible")
	}
	if _, err = s.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty: %v", err)
	}
	if _, err = s.PutSnapshot(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.PutSnapshot(canceled, req); status.Code(err) != codes.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	fresh := New()
	reply, _ = fresh.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: "person-001"})
	if reply.GetFound() {
		t.Fatal("new server must start empty")
	}
}
