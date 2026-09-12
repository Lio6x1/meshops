package edge

import (
	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"path/filepath"
	"testing"
)

func TestRetireRejectedReportPreservesLocalTerminalOutcome(t *testing.T) {
	for _, outcome := range []string{"cancel", "result"} {
		t.Run(outcome, func(t *testing.T) {
			inbox, err := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer inbox.Close()
			c := command("task-1", false)
			if _, err = inbox.Accept(c); err != nil {
				t.Fatal(err)
			}
			r := &taskv1.ReportTaskStatusRequest{EventId: "pending-acked", TaskId: "task-1", ExecutionKey: "task-1", ExecutorId: "executor", Status: commonv1.TaskStatus_TASK_STATUS_ACKED, OccurredAt: timestamppb.Now()}
			if err = inbox.StoreReport("task-1", r); err != nil {
				t.Fatal(err)
			}
			if outcome == "cancel" {
				_, err = inbox.Accept(command("task-1", true))
			} else {
				_, err = inbox.CommitResult("task-1", inspectionResult(c.Task))
			}
			if err != nil {
				t.Fatal(err)
			}
			current := proto.Clone(c.Task).(*commonv1.Task)
			current.Status = commonv1.TaskStatus_TASK_STATUS_TIMED_OUT
			foreign := proto.Clone(current).(*commonv1.Task)
			foreign.TenantId = "other"
			if err = inbox.RetireRejectedReport("task-1", r, foreign); err == nil {
				t.Fatal("foreign authority retired pending report")
			}
			if pending, err := inbox.Report("task-1"); err != nil || !proto.Equal(pending, r) {
				t.Fatal("rejected identity check changed pending fact", err)
			}
			before, err := inbox.Entry("task-1")
			if err != nil {
				t.Fatal(err)
			}
			if err = inbox.RetireRejectedReport("task-1", r, current); err != nil {
				t.Fatal(err)
			}
			after, err := inbox.Entry("task-1")
			if err != nil || after.Done || after.Outcome == 0 || after.Outcome != before.Outcome || after.Cancel != before.Cancel {
				t.Fatal("local terminal evidence lost", err)
			}
			if report, err := inbox.Report("task-1"); err != nil || report != nil {
				t.Fatal("obsolete report not retired", err)
			}
		})
	}
}
