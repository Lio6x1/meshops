package edge

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/testsupport"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type crashAckClient struct {
	ingestv1.IngestServiceClient
	t *testing.T
}
type crashAckStream struct {
	ingestv1.IngestService_ReportEntityStatesClient
	t *testing.T
}

func (c crashAckClient) ReportEntityStates(ctx context.Context, opts ...grpc.CallOption) (ingestv1.IngestService_ReportEntityStatesClient, error) {
	s, e := c.IngestServiceClient.ReportEntityStates(ctx, opts...)
	return crashAckStream{s, c.t}, e
}
func (s crashAckStream) Recv() (*ingestv1.ReportEntityStatesResponse, error) {
	r, e := s.IngestService_ReportEntityStatesClient.Recv()
	if e == nil {
		testsupport.Barrier(s.t)
	}
	return r, e
}

func TestDurableCrashChild(t *testing.T) {
	mode, dir := os.Getenv("MESHOPS_CRASH_MODE"), os.Getenv("MESHOPS_CRASH_DIR")
	switch mode {
	case "gateway_ack_before_persist":
		q, err := OpenQueue(filepath.Join(dir, "queue.db"))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			generate(t, q)
		}
		if err = os.WriteFile(filepath.Join(dir, "epoch"), []byte(q.Epoch()), 0600); err != nil {
			t.Fatal(err)
		}
		conn, err := platform.Dial(os.Getenv("MESHOPS_CRASH_ENDPOINT"))
		if err != nil {
			t.Fatal(err)
		}
		err = Upload(context.Background(), q, crashAckClient{ingestv1.NewIngestServiceClient(conn), t}, 100, 500, true, new(UplinkStats))
		t.Fatal("upload returned before barrier", err)
	case "executor_result_before_report":
		inbox, err := OpenInbox(filepath.Join(dir, "inbox.db"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := command("task-1", false)
		if _, err = inbox.Accept(cmd); err != nil {
			t.Fatal(err)
		}
		result := inspectionResult(cmd.Task)
		if _, err = inbox.CommitResult("task-1", result); err != nil {
			t.Fatal(err)
		}
		r := &taskv1.ReportTaskStatusRequest{EventId: "durable-result", TaskId: "task-1", ExecutionKey: "task-1", ExecutorId: "executor", Status: commonv1.TaskStatus_TASK_STATUS_SUCCEEDED, ResultJson: result, OccurredAt: timestamppb.Now()}
		if err = inbox.StoreReport("task-1", r); err != nil {
			t.Fatal(err)
		}
		raw, _ := proto.Marshal(r)
		if err = os.WriteFile(filepath.Join(dir, "report.pb"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		testsupport.Barrier(t)
	}
}

func TestGatewayForceKillAfterRemoteACKBeforeLocalCommit(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	receiver := new(uploadServer)
	server := grpc.NewServer()
	ingestv1.RegisterIngestServiceServer(server, receiver)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	testsupport.KillAtBarrier(t, dir, "gateway_ack_before_persist", "MESHOPS_CRASH_ENDPOINT="+listener.Addr().String())
	q, err := OpenQueue(filepath.Join(dir, "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	epoch, err := os.ReadFile(filepath.Join(dir, "epoch"))
	if err != nil || q.Epoch() != string(epoch) {
		t.Fatal("epoch lost", err)
	}
	pending, err := q.Pending(100)
	if err != nil || len(pending) != 100 {
		t.Fatal("uncertain events lost", err)
	}
	for i, item := range pending {
		if item.Sequence != int64(i+1) || !proto.Equal(item.Event, event(int64(i+1))) {
			t.Fatal("replay bytes/version changed", i)
		}
	}
	conn, err := platform.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = Upload(ctx, q, ingestv1.NewIngestServiceClient(conn), 100, 500, true, new(UplinkStats)); err != nil {
		t.Fatal(err)
	}
	stats, err := q.Stats()
	if err != nil || stats.Pending != 0 || stats.ConfirmedSequence != 100 {
		t.Fatal("replay not drained", stats, err)
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if len(receiver.requests) != 2 || !proto.Equal(receiver.requests[0], receiver.requests[1]) {
		t.Fatal("ambiguous ACK replay changed request")
	}
}
func TestExecutorForceKillRetainsSingleEffectAndExactReport(t *testing.T) {
	dir := t.TempDir()
	testsupport.KillAtBarrier(t, dir, "executor_result_before_report")
	inbox, err := OpenInbox(filepath.Join(dir, "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "report.pb"))
	if err != nil {
		t.Fatal(err)
	}
	expected := new(taskv1.ReportTaskStatusRequest)
	if err = proto.Unmarshal(raw, expected); err != nil {
		t.Fatal(err)
	}
	r, err := inbox.Report("task-1")
	if err != nil || !proto.Equal(r, expected) {
		t.Fatal("durable result report lost", err)
	}
	c := command("task-1", false)
	if fresh, err := inbox.Accept(c); err != nil || fresh {
		t.Fatal("duplicate accepted as new", err)
	}
	if changed, err := inbox.CommitResult("task-1", inspectionResult(c.Task)); err != nil || changed {
		t.Fatal("duplicate repeated effect", err)
	}
	if n, err := inbox.EffectCount("task-1"); err != nil || n != 1 {
		t.Fatal("effect count", n, err)
	}
	client := &taskClient{task: proto.Clone(c.Task).(*commonv1.Task)}
	client.task.Status = commonv1.TaskStatus_TASK_STATUS_EXECUTING
	client.task.StatusVersion = 4
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = executeOne(ctx, inbox, client, "task-1", "normal"); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 || !proto.Equal(client.requests[0], expected) {
		t.Fatal("restart failed to report exact result")
	}
}
