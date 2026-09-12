package edge

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type terminalRecoveryServer struct {
	*executionServer
	outage    atomic.Bool
	attempted chan *taskv1.ReportTaskStatusRequest
	commands  chan *executorv1.ListenTasksResponse
}

func (s *terminalRecoveryServer) ListenTasks(_ *executorv1.ListenTasksRequest, stream executorv1.ExecutorService_ListenTasksServer) error {
	for {
		select {
		case c := <-s.commands:
			if err := stream.Send(c); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *terminalRecoveryServer) ReportTaskStatus(ctx context.Context, r *taskv1.ReportTaskStatusRequest) (*taskv1.ReportTaskStatusResponse, error) {
	select {
	case s.attempted <- proto.Clone(r).(*taskv1.ReportTaskStatusRequest):
	default:
	}
	if s.outage.Load() {
		return nil, status.Error(codes.Unavailable, "Task dependency unavailable")
	}
	s.mu.Lock()
	t := s.tasks[r.TaskId]
	old := s.reports[r.EventId]
	late := t != nil && terminal(t.Status)
	if old == nil && late {
		if !terminal(r.Status) {
			s.mu.Unlock()
			return nil, status.Error(codes.FailedPrecondition, "task is terminal")
		}
		s.reports[r.EventId] = proto.Clone(r).(*taskv1.ReportTaskStatusRequest)
		response := &taskv1.ReportTaskStatusResponse{CurrentStatus: t.Status, StatusVersion: t.StatusVersion, Disposition: taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_LATE_RESULT}
		s.mu.Unlock()
		return response, nil
	}
	s.mu.Unlock()
	return s.executionServer.ReportTaskStatus(ctx, r)
}

func recoveryConnection(t *testing.T, s *terminalRecoveryServer) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(4 << 20)
	server := grpc.NewServer()
	executorv1.RegisterExecutorServiceServer(server, s)
	taskv1.RegisterTaskServiceServer(server, s)
	go server.Serve(listener)
	conn, err := grpc.NewClient("passthrough:///recovery", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); server.Stop(); listener.Close() })
	return conn
}

func TestExecutorRestartsAfterPendingNonterminalReportExpires(t *testing.T) {
	for _, reportStatus := range []commonv1.TaskStatus{commonv1.TaskStatus_TASK_STATUS_ACKED, commonv1.TaskStatus_TASK_STATUS_EXECUTING} {
		t.Run(reportStatus.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inbox.db")
			inbox, err := OpenInbox(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { inbox.Close() }()
			c := command("expired", false)
			c.Task.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
			c.Task.StatusVersion = 1
			if _, err = inbox.Accept(c); err != nil {
				t.Fatal(err)
			}
			pending := &taskv1.ReportTaskStatusRequest{EventId: "original-report", TaskId: c.Task.TaskId, ExecutionKey: c.Task.ExecutionKey, ExecutorId: c.Task.ExecutorId, DispatchId: c.DispatchId, ExpectedStatusVersion: 1, Status: reportStatus, OccurredAt: timestamppb.Now()}
			if reportStatus == commonv1.TaskStatus_TASK_STATUS_EXECUTING {
				ack := proto.Clone(pending).(*taskv1.ReportTaskStatusRequest)
				ack.EventId = "accepted-ack"
				ack.Status = commonv1.TaskStatus_TASK_STATUS_ACKED
				if err = inbox.StoreReport(c.Task.TaskId, ack); err != nil {
					t.Fatal(err)
				}
				if err = inbox.ResolveReport(c.Task.TaskId, ack, false); err != nil {
					t.Fatal(err)
				}
				c.Task.Status = commonv1.TaskStatus_TASK_STATUS_ACKED
			}
			if err = inbox.StoreReport(c.Task.TaskId, pending); err != nil {
				t.Fatal(err)
			}
			s := &terminalRecoveryServer{executionServer: &executionServer{tasks: map[string]*commonv1.Task{c.Task.TaskId: c.Task}, reports: map[string]*taskv1.ReportTaskStatusRequest{}, lost: true}, attempted: make(chan *taskv1.ReportTaskStatusRequest, 20), commands: make(chan *executorv1.ListenTasksResponse, 1)}
			s.outage.Store(true)
			conn := recoveryConnection(t, s)
			start := func() (context.CancelFunc, <-chan error) {
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() {
					done <- RunExecutor(ctx, inbox, executorv1.NewExecutorServiceClient(conn), taskv1.NewTaskServiceClient(conn), "executor", "normal", 1, new(ExecutorStats))
				}()
				return cancel, done
			}
			stop, done := start()
			select {
			case report := <-s.attempted:
				if !proto.Equal(report, pending) {
					t.Fatal("outage retry changed durable report")
				}
			case <-time.After(3 * time.Second):
				stop()
				t.Fatal("report never attempted")
			}
			stop()
			if err = <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err = inbox.Close(); err != nil {
				t.Fatal(err)
			}
			inbox, err = OpenInbox(path)
			if err != nil {
				t.Fatal(err)
			}
			stored, err := inbox.Report(c.Task.TaskId)
			if err != nil || !proto.Equal(stored, pending) {
				t.Fatal("restart lost uncertain report", err)
			}
			s.mu.Lock()
			c.Task.Status = commonv1.TaskStatus_TASK_STATUS_TIMED_OUT
			c.Task.StatusVersion++
			s.mu.Unlock()
			s.outage.Store(false)
			stop, done = start()
			defer func() { stop(); <-done }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				entry, err := inbox.Entry(c.Task.TaskId)
				if err != nil {
					t.Fatal(err)
				}
				if entry.Done {
					break
				}
				select {
				case err := <-done:
					done = closedResult(err)
					t.Fatalf("executor exited on obsolete report: %v", err)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("expired pending report did not reconcile")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if report, err := inbox.Report(c.Task.TaskId); err != nil || report != nil {
				t.Fatal("obsolete report still pending", err)
			}
			if effects, err := inbox.EffectCount(c.Task.TaskId); err != nil || effects != 0 {
				t.Fatal("expired task created an effect", effects, err)
			}
			fresh := command("fresh", false)
			fresh.Task.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
			fresh.Task.StatusVersion = 1
			s.mu.Lock()
			s.tasks[fresh.Task.TaskId] = fresh.Task
			s.mu.Unlock()
			s.commands <- fresh
			for {
				entry, err := inbox.Entry(fresh.Task.TaskId)
				if err == nil && entry.Done {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("subsequent command did not complete")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if effects, err := inbox.EffectCount(fresh.Task.TaskId); err != nil || effects != 1 {
				t.Fatal("subsequent effect count", effects, err)
			}
		})
	}
}

func closedResult(err error) <-chan error { ch := make(chan error, 1); ch <- err; return ch }
