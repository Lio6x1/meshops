//go:build integration

package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	mysql "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// With no executor stream, this still persists transport intent before send fails.
// Reports can then refer to a real dispatched attempt without advancing Task status.
func persistDispatchIntent(t *testing.T, f *fixture) {
	t.Helper()
	if e := f.dispatcher.DispatchDue(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestReportRequiresDurableDispatchIntent(t *testing.T) {
	for _, tc := range []struct {
		name, role, kind string
		status           commonv1.TaskStatus
	}{
		{"executor ACK", "executor", "execute", commonv1.TaskStatus_TASK_STATUS_ACKED},
		{"dispatcher report", "dispatcher_service", "execute", commonv1.TaskStatus_TASK_STATUS_DISPATCHED},
		{"cancellation", "executor", "cancel", commonv1.TaskStatus_TASK_STATUS_CANCELLED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixtureFor(t)
			task := create(t, f, "not-yet-dispatched")
			persistCreated(t, f, task)
			if tc.kind == "cancel" {
				if _, e := f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "cancel before delivery"}); e != nil {
					t.Fatal(e)
				}
				current, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
				if e != nil {
					t.Fatal(e)
				}
				task = current.Task
				emitTaskEvent(t, f, task, commonv1.TaskEventType_TASK_EVENT_TYPE_CANCEL_REQUESTED)
			}
			r := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.ExecutionKey, ExecutorId: task.ExecutorId, DispatchId: task.TaskId + "-" + tc.kind + "-1", ExpectedStatusVersion: task.StatusVersion, Status: tc.status, OccurredAt: timestamppb.Now()}
			if _, e := f.client.ReportTaskStatus(f.ctx("tenant", tc.role), r); status.Code(e) != codes.FailedPrecondition {
				t.Fatalf("never-dispatched report accepted: %v", e)
			}
			var receipts int
			if e := f.db.QueryRow(`SELECT COUNT(*) FROM task_execution_reports WHERE task_id=?`, task.TaskId).Scan(&receipts); e != nil || receipts != 0 {
				t.Fatalf("rejected report left receipt: %d %v", receipts, e)
			}
			current, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
			if e != nil || !proto.Equal(current.Task, task) {
				t.Fatal("rejected report changed task", e)
			}
			persistDispatchIntent(t, f)
			if _, e := f.client.ReportTaskStatus(f.ctx("tenant", tc.role), r); e != nil {
				t.Fatal("durably dispatched report rejected", e)
			}
		})
	}
}

func TestCreateAuditUsesOpaqueCorrelation(t *testing.T) {
	f := fixtureFor(t)
	key := "client-private-correlation-001"
	task := create(t, f, key)
	history, e := f.client.GetTaskHistory(f.ctx("tenant", "operator"), &taskv1.GetTaskHistoryRequest{TaskId: task.TaskId})
	if e != nil || len(history.History) != 2 {
		t.Fatal("creation audit", e)
	}
	for _, h := range history.History {
		if strings.Contains(h.Reason, key) || h.Reason == "" {
			t.Fatalf("audit retained raw key instead of correlation: %q", h.Reason)
		}
	}
	if history.History[0].Reason != history.History[1].Reason || history.History[0].EventId == history.History[1].EventId {
		t.Fatal("creation audits lost shared correlation or distinct event IDs")
	}
	if retry := create(t, f, key); retry.TaskId != task.TaskId {
		t.Fatal("audit change broke idempotency")
	}
}

type unavailableDispatch struct {
	dispatcherv1.DispatcherServiceClient
}

func (unavailableDispatch) GetDispatch(context.Context, *dispatcherv1.GetDispatchRequest, ...grpc.CallOption) (*dispatcherv1.GetDispatchResponse, error) {
	return nil, status.Error(codes.Unavailable, "test dispatch dependency unavailable")
}

func TestReportHistoricalDispatchAndDuplicateOutage(t *testing.T) {
	for _, delivery := range []string{"timeout", "dlq"} {
		t.Run(delivery, func(t *testing.T) {
			f := fixtureFor(t)
			task := create(t, f, "historical-dispatch")
			persistCreated(t, f, task)
			persistDispatchIntent(t, f)
			if _, e := f.db.Exec(`UPDATE task_dispatches SET status=? WHERE task_id=?`, delivery, task.TaskId); e != nil {
				t.Fatal(e)
			}
			r := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.ExecutionKey, ExecutorId: task.ExecutorId, DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: task.StatusVersion, Status: commonv1.TaskStatus_TASK_STATUS_ACKED, OccurredAt: timestamppb.Now()}
			applied, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), r)
			if e != nil || applied.Disposition != taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_APPLIED {
				t.Fatal("historical send intent did not authorize ACK", applied, e)
			}
			f.service.dispatcher = unavailableDispatch{}
			retry, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), r)
			if e != nil || retry.Disposition != taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_DUPLICATE || retry.StatusVersion != applied.StatusVersion {
				t.Fatal("receipt retry depended on dispatch availability", retry, e)
			}
		})
	}
}

type capturedTaskRead struct {
	taskv1.TaskServiceClient
	entered chan struct{}
	release chan struct{}
}

func (g *capturedTaskRead) GetTask(ctx context.Context, r *taskv1.GetTaskRequest, opts ...grpc.CallOption) (*taskv1.GetTaskResponse, error) {
	response, e := g.TaskServiceClient.GetTask(ctx, r, opts...)
	close(g.entered)
	select {
	case <-g.release:
		return response, e
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDispatchWorkerPreservesNewerTerminalMirror(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		name := "pending"
		if delivered {
			name = "dispatched"
		}
		t.Run(name, func(t *testing.T) {
			f := fixtureFor(t)
			task := create(t, f, "stale-dispatch-read")
			persistCreated(t, f, task)
			if delivered {
				persistDispatchIntent(t, f)
				if _, e := f.db.Exec(`UPDATE task_dispatches SET next_attempt_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE task_id=?`, task.TaskId); e != nil {
					t.Fatal(e)
				}
			}
			gate := &capturedTaskRead{TaskServiceClient: f.dispatcher.task, entered: make(chan struct{}), release: make(chan struct{})}
			f.dispatcher.task = gate
			defer func() { f.dispatcher.task = gate.TaskServiceClient }()
			var release sync.Once
			defer release.Do(func() { close(gate.release) })
			done := make(chan error, 1)
			go func() { _, e := f.dispatcher.dispatchOne(context.Background()); done <- e }()
			select {
			case <-gate.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not capture task")
			}
			if _, e := f.db.Exec(`UPDATE tasks SET deadline=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE task_id=?`, task.TaskId); e != nil {
				t.Fatal(e)
			}
			if e := f.service.ExpireTasks(context.Background()); e != nil {
				t.Fatal(e)
			}
			terminal, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
			if e != nil {
				t.Fatal(e)
			}
			emitTaskEvent(t, f, terminal.Task, commonv1.TaskEventType_TASK_EVENT_TYPE_TIMED_OUT)
			release.Do(func() { close(gate.release) })
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			attempt, e := f.dispatchClient.GetDispatch(f.ctx("tenant", "operator"), &dispatcherv1.GetDispatchRequest{TaskId: task.TaskId})
			if e != nil || attempt.Attempt != 1 || attempt.DeliveryStatus != "abandoned" || attempt.Status != commonv1.TaskStatus_TASK_STATUS_TIMED_OUT {
				t.Fatalf("stale worker overwrote terminal delivery fact: %v %v", attempt, e)
			}
		})
	}
}

func TestDispatchWorkerDefersNewerCancellationMirror(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "cancel-after-task-read")
	persistCreated(t, f, task)
	gate := &capturedTaskRead{TaskServiceClient: f.dispatcher.task, entered: make(chan struct{}), release: make(chan struct{})}
	f.dispatcher.task = gate
	defer func() { f.dispatcher.task = gate.TaskServiceClient }()
	var release sync.Once
	defer release.Do(func() { close(gate.release) })
	done := make(chan error, 1)
	go func() { _, e := f.dispatcher.dispatchOne(context.Background()); done <- e }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not capture task")
	}
	if _, e := f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "cancel raced with task lookup"}); e != nil {
		t.Fatal(e)
	}
	current, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if e != nil {
		t.Fatal(e)
	}
	// Only refresh the existing attempt's mirror. CANCEL_REQUESTED handling would
	// create a second command too; the invariant here is the pending execute row.
	tx, e := f.db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = mirrorTask(context.Background(), tx, current.Task); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	release.Do(func() { close(gate.release) })
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	var delivery string
	var sent, lease sql.NullTime
	if e = f.db.QueryRow(`SELECT status,dispatched_at,lease_until FROM task_dispatches WHERE task_id=?`, task.TaskId).Scan(&delivery, &sent, &lease); e != nil {
		t.Fatal(e)
	}
	if delivery != "pending" || sent.Valid || lease.Valid {
		t.Fatalf("stale worker ignored newer cancellation: status=%s sent=%v leased=%v", delivery, sent.Valid, lease.Valid)
	}
}

// The wrapper runs a real committed write after COUNT's rows close and before
// ListTasks starts its page query. All SQL still executes against real MySQL.
type afterCountConnector struct {
	driver.Connector
	after func()
}

func (c afterCountConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return afterCountConn{Conn: conn, after: c.after}, nil
}

type afterCountConn struct {
	driver.Conn
	after func()
}

func (c afterCountConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c afterCountConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, e := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if e == nil && strings.HasPrefix(query, "SELECT COUNT(*) FROM tasks WHERE ") {
		return &afterCountRows{Rows: rows, after: c.after}, nil
	}
	return rows, e
}

type afterCountRows struct {
	driver.Rows
	after func()
	once  sync.Once
}

func (r *afterCountRows) Close() error {
	e := r.Rows.Close()
	r.once.Do(r.after)
	return e
}

func TestListTasksCountAndPageShareSnapshot(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "list-snapshot")
	var dbName string
	if e := f.db.QueryRow(`SELECT DATABASE()`).Scan(&dbName); e != nil {
		t.Fatal(e)
	}
	cfg, e := mysql.ParseDSN(os.Getenv("MESHOPS_TEST_MYSQL_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.DBName, cfg.ParseTime, cfg.Loc = dbName, true, time.UTC
	cfg.InterpolateParams = true
	connector, e := mysql.NewConnector(cfg)
	if e != nil {
		t.Fatal(e)
	}
	var writeErr error
	hookRan := false
	db := sql.OpenDB(afterCountConnector{Connector: connector, after: func() {
		hookRan = true
		_, writeErr = f.db.Exec(`UPDATE tasks SET status='ACKED',status_version=status_version+1 WHERE task_id=?`, task.TaskId)
	}})
	defer db.Close()
	s := *f.service
	s.db = db
	response, e := s.ListTasks(f.local("tenant", "operator"), &taskv1.ListTasksRequest{Status: commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING})
	if e != nil || writeErr != nil {
		t.Fatalf("list/update: %v %v", e, writeErr)
	}
	if !hookRan {
		t.Fatal("COUNT barrier was not exercised")
	}
	if response.TotalCount != 1 || len(response.Tasks) != 1 || response.Tasks[0].TaskId != task.TaskId || response.Tasks[0].Status != commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING {
		t.Fatalf("COUNT/page mixed two committed snapshots: %v", response)
	}
}
