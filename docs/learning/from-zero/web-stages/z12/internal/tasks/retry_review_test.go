//go:build integration

package tasks

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/protobuf/types/known/timestamppb"
	"sync"
	"testing"
	"time"
)

func acknowledgeDLQ(t *testing.T, f *fixture, task *commonv1.Task, executing bool) *commonv1.Task {
	t.Helper()
	states := []commonv1.TaskStatus{commonv1.TaskStatus_TASK_STATUS_ACKED}
	if executing {
		states = append(states, commonv1.TaskStatus_TASK_STATUS_EXECUTING)
	}
	version := task.StatusVersion
	for _, state := range states {
		report := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.ExecutionKey, ExecutorId: task.ExecutorId, DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: version, Status: state, OccurredAt: timestamppb.Now()}
		response, err := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), report)
		if err != nil {
			t.Fatal(err)
		}
		version = response.StatusVersion
	}
	current, err := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if err != nil {
		t.Fatal(err)
	}
	emitTaskEvent(t, f, current.Task, eventType(current.Task.Status))
	return current.Task
}

func TestRetryDLQDeclinesAcknowledgedExecute(t *testing.T) {
	for _, executing := range []bool{false, true} {
		name := "acked"
		if executing {
			name = "executing"
		}
		t.Run(name, func(t *testing.T) {
			f := fixtureFor(t)
			task := create(t, f, "acknowledged-dlq")
			persistCreated(t, f, task)
			persistDispatchIntent(t, f)
			if _, err := f.db.Exec(`UPDATE task_dispatches SET status='dlq' WHERE task_id=?`, task.TaskId); err != nil {
				t.Fatal(err)
			}
			acknowledgeDLQ(t, f, task, executing)
			response, err := f.dispatchClient.RetryDLQ(f.ctx("tenant", "admin"), &dispatcherv1.RetryDLQRequest{TaskId: task.TaskId, Reason: "must not retry acknowledged execution"})
			if err != nil {
				t.Fatal(err)
			}
			if response.Accepted {
				t.Fatal("acknowledged execution gained a new durable retry round")
			}
			var count int
			if err = f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE task_id=?`, task.TaskId).Scan(&count); err != nil || count != 1 {
				t.Fatal("retry changed historical attempts", count, err)
			}
		})
	}
}

func TestRetryDLQRechecksAcknowledgedMirrorAfterTaskLookup(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "ack-during-retry-lookup")
	persistCreated(t, f, task)
	persistDispatchIntent(t, f)
	if _, err := f.db.Exec(`UPDATE task_dispatches SET status='dlq' WHERE task_id=?`, task.TaskId); err != nil {
		t.Fatal(err)
	}
	gate := &capturedTaskRead{TaskServiceClient: f.dispatcher.task, entered: make(chan struct{}), release: make(chan struct{})}
	f.dispatcher.task = gate
	defer func() { f.dispatcher.task = gate.TaskServiceClient }()
	var release sync.Once
	defer release.Do(func() { close(gate.release) })
	type result struct {
		response *dispatcherv1.RetryDLQResponse
		err      error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(f.local("tenant", "admin"), 10*time.Second)
	defer cancel()
	go func() {
		r, err := f.dispatcher.RetryDLQ(ctx, &dispatcherv1.RetryDLQRequest{TaskId: task.TaskId, Reason: "ACK wins while remote read in flight"})
		done <- result{r, err}
	}()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("retry did not enter remote lookup")
	}
	acknowledgeDLQ(t, f, task, false)
	release.Do(func() { close(gate.release) })
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.response.Accepted {
			t.Fatal("stale task lookup bypassed newer ACK mirror")
		}
	case <-ctx.Done():
		t.Fatal("retry did not finish")
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE task_id=?`, task.TaskId).Scan(&count); err != nil || count != 1 {
		t.Fatal("stale retry created successor", count, err)
	}
}

func TestRetryDLQStillAllowsCancellationAfterACK(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "cancel-dlq-after-ack")
	persistCreated(t, f, task)
	persistDispatchIntent(t, f)
	acknowledgeDLQ(t, f, task, false)
	if _, err := f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "cancel acknowledged execution"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if err != nil {
		t.Fatal(err)
	}
	emitTaskEvent(t, f, current.Task, commonv1.TaskEventType_TASK_EVENT_TYPE_CANCEL_REQUESTED)
	persistDispatchIntent(t, f)
	if _, err = f.db.Exec(`UPDATE task_dispatches SET status='dlq' WHERE task_id=? AND command_kind='cancel'`, task.TaskId); err != nil {
		t.Fatal(err)
	}
	response, err := f.dispatchClient.RetryDLQ(f.ctx("tenant", "admin"), &dispatcherv1.RetryDLQRequest{TaskId: task.TaskId, Reason: "retry cancellation transport"})
	if err != nil || !response.Accepted {
		t.Fatal("ACK incorrectly disabled cancellation retry", response, err)
	}
	var count int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE task_id=? AND command_kind='cancel'`, task.TaskId).Scan(&count); err != nil || count != 2 {
		t.Fatal("cancel successor missing", count, err)
	}
}
