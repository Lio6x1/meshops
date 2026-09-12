package edge

import (
	"bytes"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInboxCorruptionCannotEraseDurableEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	i, e := OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	c := command("task-1", false)
	if _, e = i.Accept(c); e != nil {
		t.Fatal(e)
	}
	if _, e = i.CommitResult("task-1", inspectionResult(c.Task)); e != nil {
		t.Fatal(e)
	}
	if e = i.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(resultsBucket) }); e != nil {
		t.Fatal(e)
	}
	i.Close()
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if broken, e := OpenInbox(path); e == nil {
		broken.Close()
		t.Fatal("corrupt inbox opened")
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("corrupt inbox changed", e)
	}
}

func command(id string, cancel bool) *executorv1.ListenTasksResponse {
	kind := executorv1.TaskCommandKind_TASK_COMMAND_KIND_EXECUTE
	suffix := "-execute"
	if cancel {
		kind = executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL
		suffix = "-cancel"
	}
	return &executorv1.ListenTasksResponse{CommandId: id + suffix, Kind: kind, DispatchId: id + suffix + "-1", Attempt: 1, Task: &commonv1.Task{TaskId: id, ExecutionKey: id, TenantId: "tenant", TargetEntityId: "person-001", ExecutorId: "executor", TaskType: "inspect", Payload: &commonv1.TaskPayload{PayloadJson: `{"duration_seconds":1}`}}}
}

// A21：重新打开数据库后，业务效果、结果与完整报告仍然保留。
func TestInboxEffectAndReportSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	i, e := OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	c := command("task-1", false)
	if fresh, e := i.Accept(c); e != nil || !fresh {
		t.Fatal(fresh, e)
	}
	result := inspectionResult(c.Task)
	if changed, e := i.CommitResult(c.Task.ExecutionKey, result); e != nil || !changed {
		t.Fatal(changed, e)
	}
	report := &taskv1.ReportTaskStatusRequest{EventId: "report-1", TaskId: c.Task.TaskId, ExecutionKey: c.Task.ExecutionKey, Status: commonv1.TaskStatus_TASK_STATUS_SUCCEEDED, ResultJson: result, OccurredAt: timestamppb.Now()}
	if e = i.StoreReport(c.Task.ExecutionKey, report); e != nil {
		t.Fatal(e)
	}
	i.Close()
	i, e = OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	if fresh, e := i.Accept(c); e != nil || fresh {
		t.Fatal(fresh, e)
	}
	if changed, e := i.CommitResult(c.Task.ExecutionKey, result); e != nil || changed {
		t.Fatal(changed, e)
	}
	count, e := i.EffectCount(c.Task.ExecutionKey)
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
	retried, e := i.Report(c.Task.ExecutionKey)
	if e != nil || !proto.Equal(report, retried) {
		t.Fatal(retried, e)
	}
}

// A22：执行前取消会创建持久化墓碑，不产生业务效果。
func TestInboxCancelBeforeExecuteSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	i, e := OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = i.Accept(command("task-1", true)); e != nil {
		t.Fatal(e)
	}
	i.Close()
	i, e = OpenInbox(path)
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	if fresh, e := i.Accept(command("task-1", false)); e != nil || fresh {
		t.Fatal(fresh, e)
	}
	if changed, e := i.CommitResult("task-1", inspectionResult(command("task-1", false).Task)); e != nil || changed {
		t.Fatal(changed, e)
	}
	entry, _ := i.Entry("task-1")
	if !entry.Cancel || entry.Outcome != int32(commonv1.TaskStatus_TASK_STATUS_CANCELLED) {
		t.Fatal(entry)
	}
	decoded, e := decodeCommand(entry)
	if e != nil || decoded.Kind != executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL {
		t.Fatal(decoded, e)
	}
}
func TestInboxCancelCompetesWithResultTransaction(t *testing.T) {
	i, e := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	for n := 0; n < 30; n++ {
		id := randomID()
		c := command(id, false)
		if _, e = i.Accept(c); e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		gate := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-gate; _, e := i.Accept(command(id, true)); errors <- e }()
		go func() { defer wg.Done(); <-gate; _, e := i.CommitResult(id, inspectionResult(c.Task)); errors <- e }()
		close(gate)
		wg.Wait()
		if e := <-errors; e != nil {
			t.Fatal(e)
		}
		if e := <-errors; e != nil {
			t.Fatal(e)
		}
		entry, _ := i.Entry(id)
		effects, _ := i.EffectCount(id)
		if entry.Outcome == int32(commonv1.TaskStatus_TASK_STATUS_SUCCEEDED) {
			if effects != 1 {
				t.Fatal(effects)
			}
		} else if entry.Outcome == int32(commonv1.TaskStatus_TASK_STATUS_CANCELLED) {
			if effects != 0 {
				t.Fatal(effects)
			}
		} else {
			t.Fatal(entry)
		}
	}
}
func TestInboxCapacityRejectsBeforeAcceptance(t *testing.T) {
	i, e := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer i.Close()
	for n := 0; n < 5; n++ {
		if _, e = i.Accept(command(string(rune('a'+n)), false)); e != nil {
			t.Fatal(e)
		}
	}
	last, e := i.Entry("e")
	if e != nil || !last.Rejected || last.Outcome != int32(commonv1.TaskStatus_TASK_STATUS_REJECTED) {
		t.Fatal(last, e)
	}
	effects, _ := i.EffectCount("e")
	if effects != 0 {
		t.Fatal(effects)
	}
}
