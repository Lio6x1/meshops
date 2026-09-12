package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type entityServer struct {
	entityv1.UnimplementedEntityServiceServer
	stream   func(*entityv1.SubscribeRequest, entityv1.EntityService_SubscribeServer) error
	snapshot func(context.Context, *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error)
}

func (s *entityServer) Subscribe(r *entityv1.SubscribeRequest, stream entityv1.EntityService_SubscribeServer) error {
	return s.stream(r, stream)
}
func (s *entityServer) GetSnapshot(ctx context.Context, r *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	return s.snapshot(ctx, r)
}

type taskServer struct {
	taskv1.UnimplementedTaskServiceServer
	create func(context.Context, *taskv1.CreateTaskRequest) (*taskv1.CreateTaskResponse, error)
}

func (s *taskServer) CreateTask(ctx context.Context, r *taskv1.CreateTaskRequest) (*taskv1.CreateTaskResponse, error) {
	return s.create(ctx, r)
}
func serve(t *testing.T, entity *entityServer, task *taskServer) string {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	if entity != nil {
		entityv1.RegisterEntityServiceServer(server, entity)
	}
	if task != nil {
		taskv1.RegisterTaskServiceServer(server, task)
	}
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}
func clientFor(t *testing.T, s *entityServer) entityv1.EntityServiceClient {
	t.Helper()
	conn, e := platform.Dial(serve(t, s, nil))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	return entityv1.NewEntityServiceClient(conn)
}
func summary(t *testing.T, out *bytes.Buffer) map[string]int {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var decoded struct {
		Summary map[string]int `json:"subscriptionSummary"`
	}
	if e := json.Unmarshal([]byte(lines[len(lines)-1]), &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded.Summary == nil {
		t.Fatalf("missing summary: %s", out)
	}
	return decoded.Summary
}
func TestSubscriptionReconnectReplacesSnapshot(t *testing.T) {
	for _, termination := range []codes.Code{codes.OK, codes.Unavailable} {
		t.Run(termination.String(), func(t *testing.T) {
			var calls atomic.Int32
			client := clientFor(t, &entityServer{stream: func(r *entityv1.SubscribeRequest, s entityv1.EntityService_SubscribeServer) error {
				n := calls.Add(1)
				if len(r.EntityIds) != 2 {
					return status.Error(codes.InvalidArgument, "IDs lost")
				}
				if n == 1 {
					if e := s.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT, EntityId: "old", Version: 1}); e != nil {
						return e
					}
				}
				if e := s.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END}); e != nil {
					return e
				}
				if n == 1 {
					if termination == codes.OK {
						return nil
					}
					return status.Error(termination, "restart")
				}
				<-s.Context().Done()
				return s.Context().Err()
			}})
			var out, diag bytes.Buffer
			e := subscribe(context.Background(), client, []string{"old", "new"}, 700*time.Millisecond, &out, &diag)
			if e != nil {
				t.Fatal(e)
			}
			got := summary(t, &out)
			if got["entityCount"] != 0 || got["reconnects"] != 1 || calls.Load() != 2 {
				t.Fatalf("summary=%v calls=%d", got, calls.Load())
			}
		})
	}
}
func TestSubscriptionDeleteRetainsVersion(t *testing.T) {
	client := clientFor(t, &entityServer{stream: func(_ *entityv1.SubscribeRequest, s entityv1.EntityService_SubscribeServer) error {
		for _, f := range []*entityv1.EntityUpdate{
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, EntityId: "e", SourceGeneration: 2, Version: 1},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE, EntityId: "e", SourceGeneration: 2, Version: 3},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, EntityId: "e", SourceGeneration: 2, Version: 2},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, EntityId: "e", SourceGeneration: 1, Version: 100},
		} {
			if e := s.Send(f); e != nil {
				return e
			}
		}
		<-s.Context().Done()
		return s.Context().Err()
	}})
	var out, diag bytes.Buffer
	if e := subscribe(context.Background(), client, []string{"e"}, 200*time.Millisecond, &out, &diag); e != nil {
		t.Fatal(e)
	}
	if got := summary(t, &out)["entityCount"]; got != 0 {
		t.Fatalf("deleted entity reinserted: %d", got)
	}
}
func TestSubscriptionRequiresCompleteSnapshot(t *testing.T) {
	client := clientFor(t, &entityServer{stream: func(_ *entityv1.SubscribeRequest, s entityv1.EntityService_SubscribeServer) error {
		if e := s.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT, EntityId: "e"}); e != nil {
			return e
		}
		<-s.Context().Done()
		return s.Context().Err()
	}})
	var out, diag bytes.Buffer
	e := subscribe(context.Background(), client, []string{"e"}, 100*time.Millisecond, &out, &diag)
	if status.Code(e) != codes.DeadlineExceeded {
		t.Fatalf("want deadline failure, got %v", e)
	}
	if strings.Contains(out.String(), "subscriptionSummary") {
		t.Fatal("reported success without snapshot end")
	}
}
func TestSubscriptionPermissionDeniedIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	client := clientFor(t, &entityServer{stream: func(_ *entityv1.SubscribeRequest, s entityv1.EntityService_SubscribeServer) error {
		calls.Add(1)
		return status.Error(codes.PermissionDenied, "forbidden")
	}})
	var out, diag bytes.Buffer
	e := subscribe(context.Background(), client, []string{"e"}, time.Second, &out, &diag)
	if status.Code(e) != codes.PermissionDenied || calls.Load() != 1 {
		t.Fatalf("error=%v calls=%d", e, calls.Load())
	}
}

func TestSubscriptionDiscardsInterruptedInitialization(t *testing.T) {
	var calls atomic.Int32
	client := clientFor(t, &entityServer{stream: func(_ *entityv1.SubscribeRequest, s entityv1.EntityService_SubscribeServer) error {
		if calls.Add(1) == 1 {
			if e := s.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT, EntityId: "partial", Version: 50}); e != nil {
				return e
			}
			return status.Error(codes.Unavailable, "interrupted initial snapshot")
		}
		for _, f := range []*entityv1.EntityUpdate{
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, EntityId: "new", SourceGeneration: 1, Version: 50},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE, EntityId: "new", SourceGeneration: 1, Version: 51},
			{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, EntityId: "new", SourceGeneration: 2, Version: 1},
		} {
			if e := s.Send(f); e != nil {
				return e
			}
		}
		<-s.Context().Done()
		return s.Context().Err()
	}})
	var out, diag bytes.Buffer
	if e := subscribe(context.Background(), client, []string{"partial", "new"}, 600*time.Millisecond, &out, &diag); e != nil {
		t.Fatal(e)
	}
	if got := summary(t, &out); got["entityCount"] != 1 || got["reconnects"] != 1 {
		t.Fatalf("summary = %v", got)
	}
}

type retryPendingClient struct {
	entityv1.EntityServiceClient
	calls int
}

func (c *retryPendingClient) Subscribe(context.Context, *entityv1.SubscribeRequest, ...grpc.CallOption) (entityv1.EntityService_SubscribeClient, error) {
	c.calls++
	return &retryPendingStream{}, nil
}

type retryPendingStream struct {
	entityv1.EntityService_SubscribeClient
	sent bool
}

func (s *retryPendingStream) Recv() (*entityv1.EntityUpdate, error) {
	if !s.sent {
		s.sent = true
		return &entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END}, nil
	}
	return nil, status.Error(codes.Unavailable, "disconnected")
}

func TestSubscriptionDoesNotCountUnattemptedRetry(t *testing.T) {
	client := &retryPendingClient{}
	var out, diag bytes.Buffer
	// 本次观测窗口短于 200ms 重试延迟，因此不可能已经完成重试等待。
	if err := subscribe(context.Background(), client, []string{"e"}, 20*time.Millisecond, &out, &diag); err != nil {
		t.Fatal(err)
	}
	if got := summary(t, &out); client.calls != 1 || got["reconnects"] != 0 {
		t.Fatalf("calls=%d summary=%v; scheduled retry is not an attempted reconnect", client.calls, got)
	}
}

func TestSubscriptionCancellationDoesNotReportSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := clientFor(t, &entityServer{stream: func(_ *entityv1.SubscribeRequest, s entityv1.EntityService_SubscribeServer) error {
		if e := s.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END}); e != nil {
			return e
		}
		cancel()
		<-s.Context().Done()
		return s.Context().Err()
	}})
	var out, diag bytes.Buffer
	if e := subscribe(ctx, client, []string{"e"}, time.Second, &out, &diag); status.Code(e) != codes.Canceled {
		t.Fatalf("want Canceled, got %v", e)
	}
	if strings.Contains(out.String(), "subscriptionSummary") {
		t.Fatal("cancellation reported success")
	}
}
func TestUsageValidatedBeforeAuthentication(t *testing.T) {
	t.Setenv("MESHOPS_OPERATOR_TOKEN", "")
	for _, args := range [][]string{
		{"snapshot"}, {"task", "create", "--entity", "e"}, {"task", "cancel", "--id", "t"},
		{"task", "list", "--page-size", "4294967296"}, {"task", "list", "--page-size", "-1"}, {"task", "list", "--status", "wrong"},
		{"history", "--entity", "e", "--start", "bad", "--end", "bad"},
		{"task", "create", "--entity", "e", "--key", "k", "--deadline", "bad"},
		{"subscribe", "--entities", ","}, {"subscribe", "--entities", "e", "--duration", "0s"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, diag bytes.Buffer
			code := Opctl(context.Background(), args, &out, &diag)
			if code != 2 || out.Len() != 0 || diag.Len() == 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diag)
			}
		})
	}
}
func TestOpctlRealRPCAndProtoJSON(t *testing.T) {
	token := strings.Repeat("x", 32)
	t.Setenv("MESHOPS_OPERATOR_TOKEN", token)
	endpoint := serve(t, &entityServer{snapshot: func(ctx context.Context, r *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if strings.Join(md.Get("authorization"), "") != "Bearer "+token {
			return nil, status.Error(codes.Unauthenticated, "missing auth")
		}
		if r.EntityId == "denied" {
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		}
		return &entityv1.GetSnapshotResponse{EntityId: r.EntityId, Version: 9007199254740993, Found: true}, nil
	}}, &taskServer{create: func(ctx context.Context, r *taskv1.CreateTaskRequest) (*taskv1.CreateTaskResponse, error) {
		var payload map[string]any
		_ = json.Unmarshal([]byte(r.GetPayload().GetPayloadJson()), &payload)
		if r.TargetEntityId != "e" || r.IdempotencyKey != "k" || payload["duration_seconds"] != float64(7) || r.Deadline == nil {
			return nil, status.Error(codes.InvalidArgument, "request mapping wrong")
		}
		return &taskv1.CreateTaskResponse{TaskId: "created", Status: commonv1.TaskStatus_TASK_STATUS_CREATED}, nil
	}})
	for _, test := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"snapshot", "--entity", "e"}, 0, `"version":"9007199254740993"`},
		{[]string{"snapshot", "--entity", "denied"}, 1, `"code":"PermissionDenied"`},
		{[]string{"task", "create", "--entity", "e", "--key", "k", "--duration-seconds", "7", "--deadline", "2030-01-01T00:00:00Z"}, 0, `"status":"TASK_STATUS_CREATED"`},
	} {
		var out, diag bytes.Buffer
		code := Opctl(context.Background(), append(test.args, "--endpoint", endpoint), &out, &diag)
		result, other := &out, &diag
		if test.code != 0 {
			result, other = &diag, &out
		}
		if code != test.code || !strings.Contains(strings.ReplaceAll(result.String(), " ", ""), test.want) || other.Len() != 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diag)
		}
	}
}
