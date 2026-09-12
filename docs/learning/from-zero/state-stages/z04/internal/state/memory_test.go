package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"net"
	"testing"
	"time"
)

func TestAuthenticatedWriteQueryAndVolatileRestart(t *testing.T) {
	r, event := ingestFixture(t)
	sourceToken := "local-source-credential-0000000000000000"
	operatorToken := "local-operator-credential-000000000000000"
	r.Credentials = map[string]string{"t:s": sourceToken, "t:op": operatorToken}
	r.Principals = map[string]platform.Principal{
		platform.Hash([]byte(sourceToken)):   {ID: "s", TenantID: "t", Role: "source", SourceID: "s"},
		platform.Hash([]byte(operatorToken)): {ID: "op", TenantID: "t", Role: "operator"},
	}
	memory := NewMemoryEntity(r)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(r.Unary()), grpc.StreamInterceptor(r.Stream()))
	entityv1.RegisterEntityServiceServer(server, memory)
	ingestv1.RegisterIngestServiceServer(server, NewIngest(platform.Settings{}, r, memory))
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := entityv1.NewEntityServiceClient(conn)
	if _, err = query.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: "p"}); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	if _, err = query.GetSnapshot(platform.Outgoing(ctx, sourceToken), &entityv1.GetSnapshotRequest{EntityId: "p"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	stream, err := ingestv1.NewIngestServiceClient(conn).ReportEntityStates(platform.Outgoing(ctx, sourceToken))
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&ingestv1.ReportEntityStatesRequest{GatewayEpoch: "test", FirstSequence: 1, Events: []*commonv1.EntityStateEvent{event}}); err != nil {
		t.Fatal(err)
	}
	ack, err := stream.Recv()
	if err != nil || ack.ConfirmedSequence != 1 {
		t.Fatal(ack, err)
	}
	stream.CloseSend()
	op := platform.Outgoing(ctx, operatorToken)
	got, err := query.GetSnapshot(op, &entityv1.GetSnapshotRequest{EntityId: "p"})
	if err != nil || !got.Found || got.Version != 1 || got.Snapshot.Power != nil {
		t.Fatal(got, err)
	}
	if _, err = query.GetSnapshot(op, &entityv1.GetSnapshotRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	missing, err := query.GetSnapshot(op, &entityv1.GetSnapshotRequest{EntityId: "missing"})
	if err != nil || missing.Found {
		t.Fatal(missing, err)
	}
	operatorCtx := platform.WithPrincipal(ctx, platform.Principal{TenantID: "t", Role: "operator"})
	restarted, err := NewMemoryEntity(r).GetSnapshot(operatorCtx, &entityv1.GetSnapshotRequest{EntityId: "p"})
	if err != nil || restarted.Found {
		t.Fatal("memory survived restart", restarted, err)
	}
}

func TestMemoryVersionAndTenantRules(t *testing.T) {
	r, event := ingestFixture(t)
	m := NewMemoryEntity(r)
	ctx := context.Background()
	publish := func(e *commonv1.EntityStateEvent) error {
		b, err := proto.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return m.Publish(ctx, "unused", "t:p", b)
	}
	event.EntityVersion = 2
	if err := publish(event); err != nil {
		t.Fatal(err)
	}
	if err := publish(event); err != nil {
		t.Fatal("duplicate", err)
	}
	old := proto.Clone(event).(*commonv1.EntityStateEvent)
	old.EntityVersion = 1
	if err := publish(old); err != nil {
		t.Fatal(err)
	}
	conflict := proto.Clone(event).(*commonv1.EntityStateEvent)
	conflict.Snapshot.Person.OnDuty = true
	if status.Code(publish(conflict)) != codes.AlreadyExists {
		t.Fatal("same-version conflict accepted")
	}
	tenantCtx := platform.WithPrincipal(ctx, platform.Principal{TenantID: "other", Role: "operator"})
	got, err := m.GetSnapshot(tenantCtx, &entityv1.GetSnapshotRequest{EntityId: "p"})
	if err != nil || got.Found {
		t.Fatal("tenant leakage", got, err)
	}
	deleted := proto.Clone(event).(*commonv1.EntityStateEvent)
	deleted.EntityVersion = 3
	deleted.EventId = "deleted"
	deleted.Operation = commonv1.EntityOperation_ENTITY_OPERATION_DELETE
	deleted.Snapshot = nil
	if err := publish(deleted); err != nil {
		t.Fatal(err)
	}
	if err := publish(event); err != nil {
		t.Fatal(err)
	}
	if m.events["t:p"].Operation != commonv1.EntityOperation_ENTITY_OPERATION_DELETE {
		t.Fatal("replay resurrected tombstone")
	}
}
