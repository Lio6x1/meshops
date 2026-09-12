package edge

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This fixture uses real gRPC streams and concurrent unary calls; its mutex is
// only server fixture state, never the implementation under test's persistence.
type executionServer struct {
	executorv1.UnimplementedExecutorServiceServer
	taskv1.UnimplementedTaskServiceServer
	mu                       sync.Mutex
	tasks                    map[string]*commonv1.Task
	reports                  map[string]*taskv1.ReportTaskStatusRequest
	listens, active, maximum int
	lost                     bool
}

func (s *executionServer) ListenTasks(_ *executorv1.ListenTasksRequest, stream executorv1.ExecutorService_ListenTasksServer) error {
	s.mu.Lock()
	s.listens++
	n := s.listens
	s.mu.Unlock()
	for i := 0; i < 5; i++ {
		if err := stream.Send(command(fmt.Sprintf("task-%d", i), false)); err != nil {
			return err
		}
	}
	if n == 1 {
		return status.Error(codes.Unavailable, "transport interruption after delivery")
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}
func (s *executionServer) GetTask(_ context.Context, r *taskv1.GetTaskRequest) (*taskv1.GetTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[r.TaskId]
	if task == nil {
		return nil, status.Error(codes.NotFound, "task")
	}
	return &taskv1.GetTaskResponse{Task: proto.Clone(task).(*commonv1.Task)}, nil
}
func (s *executionServer) ReportTaskStatus(_ context.Context, r *taskv1.ReportTaskStatusRequest) (*taskv1.ReportTaskStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[r.TaskId]
	if prior := s.reports[r.EventId]; prior != nil {
		if !proto.Equal(prior, r) {
			return nil, status.Error(codes.AlreadyExists, "changed report identity")
		}
		return &taskv1.ReportTaskStatusResponse{CurrentStatus: task.Status, StatusVersion: task.StatusVersion, Disposition: taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_DUPLICATE}, nil
	}
	if task.StatusVersion != r.ExpectedStatusVersion {
		return nil, status.Error(codes.Aborted, "version")
	}
	if r.Status == commonv1.TaskStatus_TASK_STATUS_EXECUTING {
		s.active++
		if s.active > s.maximum {
			s.maximum = s.active
		}
	}
	if r.Status == commonv1.TaskStatus_TASK_STATUS_SUCCEEDED {
		s.active--
	}
	task.Status = r.Status
	task.StatusVersion++
	s.reports[r.EventId] = proto.Clone(r).(*taskv1.ReportTaskStatusRequest)
	if !s.lost && r.Status == commonv1.TaskStatus_TASK_STATUS_ACKED {
		s.lost = true
		return nil, status.Error(codes.Unavailable, "applied response was lost")
	}
	return &taskv1.ReportTaskStatusResponse{CurrentStatus: task.Status, StatusVersion: task.StatusVersion, Disposition: taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_APPLIED}, nil
}
func TestExecutorReconnectBoundedWorkersAndAppliedResponseLoss(t *testing.T) {
	s := &executionServer{tasks: map[string]*commonv1.Task{}, reports: map[string]*taskv1.ReportTaskStatusRequest{}}
	for n := 0; n < 5; n++ {
		c := command(fmt.Sprintf("task-%d", n), false)
		c.Task.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
		c.Task.StatusVersion = 1
		s.tasks[c.Task.TaskId] = c.Task
	}
	listener := bufconn.Listen(4 << 20)
	server := grpc.NewServer()
	executorv1.RegisterExecutorServiceServer(server, s)
	taskv1.RegisterTaskServiceServer(server, s)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	conn, e := grpc.NewClient("passthrough:///memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	inbox, e := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer inbox.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	stats := new(ExecutorStats)
	go func() {
		done <- RunExecutor(ctx, inbox, executorv1.NewExecutorServiceClient(conn), taskv1.NewTaskServiceClient(conn), "executor", "normal", 4, stats)
	}()
	for {
		accepted, completed, effects, e := inbox.Stats()
		if e != nil {
			t.Fatal(e)
		}
		if completed == 5 {
			if accepted != 4 || effects != 4 {
				t.Fatal(accepted, completed, effects)
			}
			break
		}
		if e = pause(ctx, 25*time.Millisecond); e != nil {
			t.Fatal("executor did not complete:", e)
		}
	}
	cancel()
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listens < 2 || stats.DuplicateCommands.Load() < 5 || s.maximum > 4 {
		t.Fatal(s.listens, stats.DuplicateCommands.Load(), s.maximum)
	}
	for n := 0; n < 4; n++ {
		count, e := inbox.EffectCount(fmt.Sprintf("task-%d", n))
		if e != nil || count != 1 {
			t.Fatal(count, e)
		}
	}
	if s.tasks["task-4"].Status != commonv1.TaskStatus_TASK_STATUS_REJECTED {
		t.Fatal("overflow task was not rejected")
	}
}
