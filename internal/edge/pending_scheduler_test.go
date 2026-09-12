package edge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestPendingPageExclusiveBoundarySurvivesDeletionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	i, err := OpenInbox(path)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 130; n++ {
		if _, err = i.Accept(command(fmt.Sprintf("task-%03d", n), true)); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, -1, 65} {
		if _, err = i.PendingPage("", limit); err == nil {
			t.Fatalf("invalid page limit accepted %d", limit)
		}
	}
	seen := map[string]bool{}
	after := ""
	for {
		keys, e := i.PendingPage(after, 7)
		if e != nil {
			t.Fatal(e)
		}
		if len(keys) == 0 {
			break
		}
		if len(keys) > 7 {
			t.Fatal("unbounded page")
		}
		for _, key := range keys {
			if key <= after || seen[key] {
				t.Fatal("nonexclusive or repeated key", key, after)
			}
			seen[key] = true
			after = key
		}
		// The next seek must still include its first greater key after removal
		// of the previous boundary; blindly Seek+Next would skip that key.
		if err = i.db.Update(func(tx *bolt.Tx) error {
			v, e := getEntry(tx, after)
			if e != nil {
				return e
			}
			v.Done = true
			return putEntry(tx, after, v)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 130 {
		t.Fatal("lost pending entries", len(seen))
	}
	if _, err = i.Accept(command("aaa-inserted-behind-cursor", true)); err != nil {
		t.Fatal(err)
	}
	i.Close()
	i, err = OpenInbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close()
	keys, err := i.PendingPage("", 7)
	if err != nil || len(keys) != 7 || keys[0] != "aaa-inserted-behind-cursor" {
		t.Fatal("restart did not rebuild bounded index", keys, err)
	}
}

func TestPendingRoundFencePreventsGrowingTailFromStarvingEarlierKeys(t *testing.T) {
	i, err := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close()
	for _, key := range []string{"middle-a", "middle-b"} {
		if _, err = i.Accept(command(key, true)); err != nil {
			t.Fatal(err)
		}
	}
	fence, err := i.pendingBoundary()
	if err != nil {
		t.Fatal(err)
	}
	if fence != "middle-b" {
		t.Fatal(fence)
	}
	if _, err = i.Accept(command("aaa-late", true)); err != nil {
		t.Fatal(err)
	}
	if _, err = i.Accept(command("zzz-late", true)); err != nil {
		t.Fatal(err)
	}
	keys, err := i.pendingPage("middle-a", fence, 4)
	if err != nil || len(keys) != 1 || keys[0] != "middle-b" {
		t.Fatal("new tail extended current round", keys, err)
	}
	keys, err = i.pendingPage("middle-b", fence, 4)
	if err != nil || len(keys) != 0 {
		t.Fatal("round cannot finish", keys, err)
	}
	fence, err = i.pendingBoundary()
	if err != nil {
		t.Fatal(err)
	}
	keys, err = i.pendingPage("", fence, 4)
	if err != nil || len(keys) != 4 || keys[0] != "aaa-late" || keys[3] != "zzz-late" {
		t.Fatal("next round lost inserts", keys, err)
	}
}

type backlogExecutionServer struct {
	*executionServer
	delivery chan *executorv1.ListenTasksResponse
	observed chan string
	dropped  bool
	retried  bool
	first    *taskv1.ReportTaskStatusRequest
}

func (s *backlogExecutionServer) ListenTasks(_ *executorv1.ListenTasksRequest, stream executorv1.ExecutorService_ListenTasksServer) error {
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case c := <-s.delivery:
			if err := stream.Send(c); err != nil {
				return err
			}
		}
	}
}
func (s *backlogExecutionServer) ReportTaskStatus(ctx context.Context, r *taskv1.ReportTaskStatusRequest) (*taskv1.ReportTaskStatusResponse, error) {
	response, err := s.executionServer.ReportTaskStatus(ctx, r)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	drop := false
	if !s.dropped {
		s.dropped = true
		s.first = proto.Clone(r).(*taskv1.ReportTaskStatusRequest)
		drop = true
	} else if r.EventId == s.first.EventId {
		s.retried = proto.Equal(r, s.first)
	}
	s.mu.Unlock()
	select {
	case s.observed <- r.TaskId:
	default:
	}
	if drop {
		return nil, status.Error(codes.Unavailable, "applied terminal response lost")
	}
	return response, nil
}

func TestExecutorDrainsRestartedBacklogAndWrapsForNewLowerKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	i, err := OpenInbox(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &backlogExecutionServer{executionServer: &executionServer{tasks: map[string]*commonv1.Task{}, reports: map[string]*taskv1.ReportTaskStatusRequest{}, lost: true}, delivery: make(chan *executorv1.ListenTasksResponse, 4), observed: make(chan string, 200)}
	for n := 0; n < 80; n++ {
		c := command(fmt.Sprintf("task-%03d", n), true)
		if _, err = i.Accept(c); err != nil {
			t.Fatal(err)
		}
		s.tasks[c.Task.TaskId] = proto.Clone(c.Task).(*commonv1.Task)
	}
	i.Close()
	i, err = OpenInbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer i.Close()
	listener := bufconn.Listen(4 << 20)
	server := grpc.NewServer()
	executorv1.RegisterExecutorServiceServer(server, s)
	taskv1.RegisterTaskServiceServer(server, s)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	conn, err := grpc.NewClient("passthrough:///memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunExecutor(ctx, i, executorv1.NewExecutorServiceClient(conn), taskv1.NewTaskServiceClient(conn), "executor", "normal", 2, new(ExecutorStats))
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	for n := 0; n < 5; n++ {
		select {
		case <-s.observed:
		case <-ctx.Done():
			t.Fatal("restored backlog not scheduled")
		}
	}
	late := command("aaa-new", true)
	s.mu.Lock()
	s.tasks[late.Task.TaskId] = proto.Clone(late.Task).(*commonv1.Task)
	s.mu.Unlock()
	s.delivery <- late
	s.delivery <- late
	for {
		_, completed, effects, e := i.Stats()
		if e != nil {
			t.Fatal(e)
		}
		if completed == 81 {
			if effects != 0 {
				t.Fatal("cancellation produced effect")
			}
			break
		}
		if e = pause(ctx, 10*time.Millisecond); e != nil {
			t.Fatal("backlog or lower-key insertion starved", completed, e)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.retried {
		t.Fatal("uncertain durable report did not retry exact identity")
	}
	if len(s.reports) != 81 {
		t.Fatal("duplicate delivery added a second report", len(s.reports))
	}
}
