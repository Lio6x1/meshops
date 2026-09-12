//go:build integration

package tasks

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

type blockedDispatch struct {
	dispatcherv1.DispatcherServiceClient
	entered chan struct{}
	release chan struct{}
}

func (b *blockedDispatch) GetDispatch(ctx context.Context, r *dispatcherv1.GetDispatchRequest, opts ...grpc.CallOption) (*dispatcherv1.GetDispatchResponse, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.DispatcherServiceClient.GetDispatch(ctx, r, opts...)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestReportRemoteValidationDoesNotHoldTaskLock(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "no-network-lock")
	persistCreated(t, f, task)
	persistDispatchIntent(t, f)
	block := &blockedDispatch{DispatcherServiceClient: f.service.dispatcher, entered: make(chan struct{}), release: make(chan struct{})}
	f.service.dispatcher = block
	finished := make(chan error, 1)
	go func() {
		_, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.TaskId, ExecutorId: "executor", DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: 1, Status: commonv1.TaskStatus_TASK_STATUS_ACKED, OccurredAt: timestamppb.Now()})
		finished <- e
	}()
	select {
	case <-block.entered:
	case <-time.After(time.Second):
		t.Fatal("report never entered remote validation")
	}
	ctx, cancel := context.WithTimeout(f.ctx("tenant", "operator"), time.Second)
	_, e := f.client.CancelTask(ctx, &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "must not block on dispatch lookup"})
	cancel()
	close(block.release)
	if e != nil {
		t.Fatal("cancel was blocked by report's remote call", e)
	}
	if e = <-finished; status.Code(e) != codes.Aborted {
		t.Fatal("version was not rechecked after unlocked remote validation", e)
	}
}

type blockedBus struct{ started chan struct{} }

func (b *blockedBus) Publish(ctx context.Context, topic, key string, raw []byte) error {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}
func (b *blockedBus) Consume(ctx context.Context, group, topic string, h func(context.Context, []byte) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestDeadlineWorkerContinuesDuringKafkaStall(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "deadline-independent")
	if _, e := f.db.Exec(`UPDATE tasks SET deadline=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE task_id=?`, task.TaskId); e != nil {
		t.Fatal(e)
	}
	bus := &blockedBus{started: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.service.Run(ctx, bus) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker shutdown did not join promptly")
		}
	}()
	select {
	case <-bus.started:
	case <-time.After(time.Second):
		t.Fatal("publication did not start")
	}
	deadline := time.Now().Add(2500 * time.Millisecond)
	for {
		r, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
		if e != nil {
			t.Fatal(e)
		}
		if r.Task.Status == commonv1.TaskStatus_TASK_STATUS_TIMED_OUT {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Kafka's five-second publication timeout stalled one-second task expiry")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
