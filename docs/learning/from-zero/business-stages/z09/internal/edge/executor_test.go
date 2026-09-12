package edge

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"path/filepath"
	"testing"
	"time"
)

type taskClient struct {
	taskv1.TaskServiceClient
	task     *commonv1.Task
	requests []*taskv1.ReportTaskStatusRequest
	lost     bool
	abort    bool
}

func (c *taskClient) GetTask(context.Context, *taskv1.GetTaskRequest, ...grpc.CallOption) (*taskv1.GetTaskResponse, error) {
	return &taskv1.GetTaskResponse{Task: proto.Clone(c.task).(*commonv1.Task)}, nil
}
func (c *taskClient) ReportTaskStatus(_ context.Context, r *taskv1.ReportTaskStatusRequest, _ ...grpc.CallOption) (*taskv1.ReportTaskStatusResponse, error) {
	c.requests = append(c.requests, proto.Clone(r).(*taskv1.ReportTaskStatusRequest))
	if c.abort {
		c.abort = false
		c.task.StatusVersion++
		return nil, status.Error(codes.Aborted, "concurrent DISPATCHED")
	}
	if c.lost {
		c.lost = false
		return nil, status.Error(codes.Unavailable, "response lost")
	}
	c.task.Status = r.Status
	c.task.StatusVersion++
	return &taskv1.ReportTaskStatusResponse{CurrentStatus: r.Status, StatusVersion: c.task.StatusVersion, Disposition: taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_APPLIED}, nil
}
func TestExecutorReportsSeriallyAndRetriesExactIdentity(t *testing.T) {
	i, e := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	cmd := command("task-1", false)
	if _, e = i.Accept(cmd); e != nil {
		t.Fatal(e)
	}
	client := &taskClient{task: proto.Clone(cmd.Task).(*commonv1.Task), lost: true, abort: true}
	client.task.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
	client.task.StatusVersion = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = executeOne(ctx, i, client, "task-1", "normal"); e != nil {
		t.Fatal(e)
	}
	r := client.requests
	if len(r) != 5 {
		t.Fatal(len(r))
	}
	if r[0].EventId == r[1].EventId {
		t.Fatal("ABORTED retained old identity despite changed expected version")
	}
	if !proto.Equal(r[1], r[2]) {
		t.Fatal("uncertain response changed retry identity/content")
	}
	want := []commonv1.TaskStatus{commonv1.TaskStatus_TASK_STATUS_ACKED, commonv1.TaskStatus_TASK_STATUS_EXECUTING, commonv1.TaskStatus_TASK_STATUS_SUCCEEDED}
	for n, s := range want {
		if r[n+2].Status != s {
			t.Fatal(r)
		}
	}
	effects, e := i.EffectCount("task-1")
	if e != nil || effects != 1 {
		t.Fatal(effects, e)
	}
	if r[4].ResultJson != inspectionResult(cmd.Task) {
		t.Fatal(r[4])
	}
}
func TestExecutorCancellationUsesCancelDispatch(t *testing.T) {
	i, e := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	cmd := command("task-1", true)
	if _, e = i.Accept(cmd); e != nil {
		t.Fatal(e)
	}
	client := &taskClient{task: cmd.Task}
	client.task.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCHED
	client.task.StatusVersion = 2
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = executeOne(ctx, i, client, "task-1", "normal"); e != nil {
		t.Fatal(e)
	}
	if len(client.requests) != 1 || client.requests[0].Status != commonv1.TaskStatus_TASK_STATUS_CANCELLED || client.requests[0].DispatchId != "task-1-cancel-1" {
		t.Fatal(client.requests)
	}
	effects, _ := i.EffectCount("task-1")
	if effects != 0 {
		t.Fatal(effects)
	}
}
func TestCLIRejectsMissingAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"--db", "x", "--offline", "--drain-only"}, {"--db", "x", "--rate", "0"}} {
		if code := GatewayCLI(context.Background(), args, ioDiscard{}, ioDiscard{}); code != 2 {
			t.Fatal(code)
		}
	}
	if code := ExecutorCLI(context.Background(), []string{"--concurrency", "5"}, ioDiscard{}, ioDiscard{}); code != 2 {
		t.Fatal(code)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestExecutorRestartAfterResultBeforeReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	inbox, e := OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	cmd := command("task-1", false)
	if _, e = inbox.Accept(cmd); e != nil {
		t.Fatal(e)
	}
	if _, e = inbox.CommitResult("task-1", inspectionResult(cmd.Task)); e != nil {
		t.Fatal(e)
	}
	inbox.Close()
	inbox, e = OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	defer inbox.Close()
	client := &taskClient{task: proto.Clone(cmd.Task).(*commonv1.Task)}
	client.task.Status = commonv1.TaskStatus_TASK_STATUS_EXECUTING
	client.task.StatusVersion = 4
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = executeOne(ctx, inbox, client, "task-1", "normal"); e != nil {
		t.Fatal(e)
	}
	if len(client.requests) != 1 || client.requests[0].Status != commonv1.TaskStatus_TASK_STATUS_SUCCEEDED || client.requests[0].ResultJson != inspectionResult(cmd.Task) {
		t.Fatal(client.requests)
	}
	effects, e := inbox.EffectCount("task-1")
	if e != nil || effects != 1 {
		t.Fatal(effects, e)
	}
}
