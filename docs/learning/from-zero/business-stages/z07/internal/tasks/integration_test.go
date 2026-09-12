//go:build integration

package tasks

import (
	"context"
	"database/sql"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	mysql "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests require a real MySQL8 server. Missing infrastructure is a failure
// of this explicit integration target, never a skipped acceptance pass.
func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MESHOPS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Fatal("MESHOPS_TEST_MYSQL_DSN must name an isolated MySQL8 server account with CREATE DATABASE and EVENT privileges")
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal("invalid MESHOPS_TEST_MYSQL_DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	admin, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	name := "course_test_" + strings.ReplaceAll(platform.NewID(), "-", "")
	if _, e = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4"); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	cfg.DBName = name
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(30)
	t.Cleanup(func() {
		db.Close()
		if !strings.HasPrefix(name, "course_test_") || len(name) != 44 {
			t.Errorf("refusing unexpected cleanup database %q", name)
			return
		}
		if _, e := admin.Exec("DROP DATABASE `" + name + "`"); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	return db
}
func execSQLFile(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	statements, e := SplitSQL(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range statements {
		if _, e = db.Exec(s); e != nil {
			t.Fatal(e)
		}
	}
}
func TestA03MigrationsAndNonDestructiveSeed(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			db := isolatedDB(t)
			if legacy {
				execSQLFile(t, db, "../../migrations/001_initial_schema.sql")
				execSQLFile(t, db, "../../testdata/migrations/001_existing_task.sql")
				execSQLFile(t, db, "../../migrations/002_framework_contracts.sql")
			}
			if e := Migrate(context.Background(), db, "../../migrations"); e != nil {
				t.Fatal(e)
			}
			if e := Migrate(context.Background(), db, "../../migrations"); e != nil {
				t.Fatal("repeat migration:", e)
			}
			if legacy {
				var key string
				if e := db.QueryRow(`SELECT execution_key FROM tasks WHERE task_id='legacy-task'`).Scan(&key); e != nil || key != "legacy-task" {
					t.Fatalf("legacy execution identity lost: %q %v", key, e)
				}
				var n int
				db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id='legacy-task' AND published_at IS NULL`).Scan(&n)
				if n != 1 {
					t.Fatal("legacy pending outbox lost")
				}
			}
			reg := testRegistry()
			if e := Seed(context.Background(), db, reg); e != nil {
				t.Fatal(e)
			}
			if e := Seed(context.Background(), db, reg); e != nil {
				t.Fatal("repeat seed:", e)
			}
			if e := CheckBindings(context.Background(), db, reg); e != nil {
				t.Fatal(e)
			}
			binding := reg.Bindings["tenant:drone-001"]
			binding.SourceGeneration++
			reg.Bindings["tenant:drone-001"] = binding
			if e := Seed(context.Background(), db, reg); e == nil {
				t.Fatal("conflicting seed succeeded")
			}
			var generation int
			if e := db.QueryRow(`SELECT source_generation FROM entities WHERE tenant_id='tenant' AND entity_id='drone-001'`).Scan(&generation); e != nil || generation != 1 {
				t.Fatal("conflict changed authoritative row", generation, e)
			}
		})
	}
	t.Run("duplicate audit blocks upgrade", func(t *testing.T) {
		db := isolatedDB(t)
		execSQLFile(t, db, "../../migrations/001_initial_schema.sql")
		execSQLFile(t, db, "../../migrations/002_framework_contracts.sql")
		for i := 0; i < 2; i++ {
			if _, e := db.Exec(`INSERT INTO task_status_history(tenant_id,task_id,from_status,to_status,status_version,changed_by) VALUES('tenant','old-task','CREATED','DISPATCH_PENDING',1,'test')`); e != nil {
				t.Fatal(e)
			}
		}
		e := Migrate(context.Background(), db, "../../migrations")
		if e == nil || !strings.Contains(e.Error(), "duplicate legacy audits") {
			t.Fatal("duplicate legacy audits were not rejected", e)
		}
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM task_status_history`).Scan(&n)
		if n != 2 {
			t.Fatal("upgrade deleted conflicting evidence")
		}
	})
}
func testRegistry() *platform.Registry {
	r := &platform.Registry{Bindings: map[string]platform.Binding{}, Sources: map[string]platform.Source{}, Principals: map[string]platform.Principal{}, Credentials: map[string]string{}}
	for _, tenant := range []string{"tenant", "other"} {
		source := tenant + "_drone"
		r.Bindings[tenant+":drone-001"] = platform.Binding{TenantID: tenant, EntityID: "drone-001", Type: "drone", SourceID: source, SourceGeneration: 1, ExecutorID: "executor", Tasks: []string{"inspect"}}
		r.Sources[tenant+":"+source] = platform.Source{TenantID: tenant, ID: source, Adapter: "drone", Generation: 1, StaleAfter: 30 * time.Second, Rate: 100, Entities: map[string]string{"drone-001": "drone-001"}}
		for _, role := range []string{"operator", "admin", "task_service", "dispatcher_service", "executor", "source"} {
			p := platform.Principal{ID: role, TenantID: tenant, Role: role}
			if role == "source" {
				p.ID = source
				p.SourceID = source
			}
			if role == "executor" {
				p.ExecutorID = "executor"
				p.EntityIDs = []string{"drone-001"}
			}
			token := strings.Repeat("x", 32) + tenant + role
			r.Principals[digest([]byte(token))] = p
			r.Credentials[platform.Key(tenant, p.ID)] = token
		}
	}
	return r
}

type snapshotStub struct {
	entityv1.UnimplementedEntityServiceServer
	offline atomic.Bool
}

func (s *snapshotStub) GetSnapshot(ctx context.Context, r *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	if s.offline.Load() {
		return &entityv1.GetSnapshotResponse{Found: false}, nil
	}
	return &entityv1.GetSnapshotResponse{EntityId: r.EntityId, Found: true, ExecutorId: "executor", ExpiresAt: timestamppb.New(time.Now().Add(time.Minute)), Snapshot: &commonv1.EntitySnapshot{Status: "idle", Capability: &commonv1.Capability{SupportedTasks: []string{"inspect"}}, TaskCatalog: &commonv1.TaskCatalog{Definitions: []*commonv1.TaskDefinition{{TaskType: "inspect"}}}}}, nil
}

type fixture struct {
	executorClient executorv1.ExecutorServiceClient
	db             *sql.DB
	service        *Service
	dispatcher     *Dispatcher
	client         taskv1.TaskServiceClient
	dispatchClient dispatcherv1.DispatcherServiceClient
	reg            *platform.Registry
	entity         *snapshotStub
}

func fixtureFor(t *testing.T) *fixture {
	t.Helper()
	db := isolatedDB(t)
	if e := Migrate(context.Background(), db, "../../migrations"); e != nil {
		t.Fatal(e)
	}
	reg := testRegistry()
	if e := Seed(context.Background(), db, reg); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	conn, e := platform.Dial(listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("COURSE_TEST_CURSOR", strings.Repeat("c", 32))
	cfg := platform.Settings{CursorKeyEnv: "COURSE_TEST_CURSOR", OutboxPoll: "1ms", OutboxLease: "30s", AckTimeout: "30s", MaxAttempts: 5, RetryDelays: []string{"5s", "30s", "300s", "300s"}}
	service, e := NewService(cfg, reg, db, entityv1.NewEntityServiceClient(conn), dispatcherv1.NewDispatcherServiceClient(conn))
	if e != nil {
		t.Fatal(e)
	}
	dispatcher, e := NewDispatcher(cfg, reg, db, taskv1.NewTaskServiceClient(conn))
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(reg.Unary()), grpc.StreamInterceptor(reg.Stream()))
	taskv1.RegisterTaskServiceServer(server, service)
	dispatcherv1.RegisterDispatcherServiceServer(server, dispatcher)
	executorv1.RegisterExecutorServiceServer(server, dispatcher)
	entity := &snapshotStub{}
	entityv1.RegisterEntityServiceServer(server, entity)
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); conn.Close() })
	return &fixture{db: db, service: service, dispatcher: dispatcher, client: taskv1.NewTaskServiceClient(conn), dispatchClient: dispatcherv1.NewDispatcherServiceClient(conn), executorClient: executorv1.NewExecutorServiceClient(conn), reg: reg, entity: entity}
}
func (f *fixture) ctx(tenant, role string) context.Context {
	token, _ := f.reg.Credential(tenant, role)
	return platform.Outgoing(context.Background(), token)
}
func (f *fixture) local(tenant, role string) context.Context {
	token, _ := f.reg.Credential(tenant, role)
	p, _ := f.reg.Authenticate(token)
	return platform.WithPrincipal(context.Background(), p)
}
func request(key string) *taskv1.CreateTaskRequest {
	return &taskv1.CreateTaskRequest{IdempotencyKey: key, TaskType: "inspect", TargetEntityId: "drone-001", Payload: &commonv1.TaskPayload{PayloadJson: `{"duration_seconds":5}`}}
}
func create(t *testing.T, f *fixture, key string) *commonv1.Task {
	t.Helper()
	r, e := f.client.CreateTask(f.ctx("tenant", "operator"), request(key))
	if e != nil {
		t.Fatal(e)
	}
	get, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: r.TaskId})
	if e != nil {
		t.Fatal(e)
	}
	return get.Task
}
func TestA15A16A17TaskTransactionsAndIsolation(t *testing.T) {
	f := fixtureFor(t)
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.client.CreateTask(f.ctx("tenant", "operator"), request("same-key"))
			if e != nil {
				errs <- e
				return
			}
			ids <- r.TaskId
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("idempotent creation returned different IDs")
		}
		id = got
	}
	if id == "" {
		t.Fatal("no task created")
	}
	for table, want := range map[string]int{"tasks": 1, "task_status_history": 2, "outbox_events": 1} {
		var got int
		if e := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); e != nil || got != want {
			t.Fatalf("%s count=%d want=%d: %v", table, got, want, e)
		}
	}
	f.entity.offline.Store(true)
	if r, e := f.client.CreateTask(f.ctx("tenant", "operator"), request("same-key")); e != nil || r.TaskId != id {
		t.Fatal("idempotent retry incorrectly rechecked unavailable snapshot", e)
	}
	different := request("same-key")
	different.Payload.PayloadJson = `{"duration_seconds":6}`
	if _, e := f.client.CreateTask(f.ctx("tenant", "operator"), different); status.Code(e) != codes.AlreadyExists {
		t.Fatal("conflicting key", e)
	}
	f.entity.offline.Store(false)
	if _, e := f.client.GetTask(f.ctx("other", "operator"), &taskv1.GetTaskRequest{TaskId: id}); status.Code(e) != codes.NotFound {
		t.Fatal("cross tenant task leaked", e)
	}
	if _, e := f.db.Exec(`CREATE TRIGGER fail_outbox BEFORE INSERT ON outbox_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected transaction failure'`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.client.CreateTask(f.ctx("tenant", "operator"), request("must-rollback")); status.Code(e) != codes.Unavailable {
		t.Fatal("injected failure should not succeed", e)
	}
	for table, want := range map[string]int{"tasks": 1, "task_status_history": 2, "outbox_events": 1} {
		var n int
		f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		if n != want {
			t.Fatalf("partial commit in %s", table)
		}
	}
	if _, e := f.db.Exec(`DROP TRIGGER fail_outbox`); e != nil {
		t.Fatal(e)
	}
	task, _ := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: id})
	persistCreated(t, f, task.Task)
	report := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: id, ExecutionKey: id, ExecutorId: "executor", DispatchId: id + "-execute-1", ExpectedStatusVersion: 1, Status: commonv1.TaskStatus_TASK_STATUS_ACKED, OccurredAt: timestamppb.Now()}
	competing := proto.Clone(report).(*taskv1.ReportTaskStatusRequest)
	competing.EventId = platform.NewID()
	competing.Status = commonv1.TaskStatus_TASK_STATUS_REJECTED
	outcomes := make(chan error, 2)
	for _, r := range []*taskv1.ReportTaskStatusRequest{report, competing} {
		go func(r *taskv1.ReportTaskStatusRequest) {
			_, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), r)
			outcomes <- e
		}(r)
	}
	a, b := <-outcomes, <-outcomes
	if a != nil && b != nil {
		t.Fatalf("no concurrent report applied: %v %v", a, b)
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM task_status_history WHERE task_id=? AND status_version=2`, id).Scan(&n)
	if n != 1 {
		t.Fatal("competing reports both advanced")
	}
	// Whichever report was accepted can be replayed with its original expected
	// version. The receipt lookup must precede old-version validation.
	var winner string
	f.db.QueryRow(`SELECT event_id FROM task_execution_reports WHERE disposition='applied' AND task_id=?`, id).Scan(&winner)
	accepted := report
	if winner == competing.EventId {
		accepted = competing
	}
	replayed, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), accepted)
	if e != nil || replayed.Disposition != taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_DUPLICATE {
		t.Fatal("stable receipt was not duplicate", e)
	}
	history, e := f.client.GetTaskHistory(f.ctx("tenant", "operator"), &taskv1.GetTaskHistoryRequest{TaskId: id})
	if e != nil || len(history.History) != 3 {
		t.Fatal("history mismatch", e)
	}
	for i, h := range history.History {
		if h.StatusVersion != int32(i) {
			t.Fatal("history not version ordered")
		}
	}
}
func persistCreated(t *testing.T, f *fixture, task *commonv1.Task) {
	t.Helper()
	data, e := protojson.Marshal(task)
	if e != nil {
		t.Fatal(e)
	}
	event := &commonv1.TaskEvent{EventId: platform.NewID(), TaskId: task.TaskId, TenantId: task.TenantId, CurrentStatus: task.Status, StatusVersion: task.StatusVersion, EventType: commonv1.TaskEventType_TASK_EVENT_TYPE_CREATED, DataJson: string(data)}
	raw, _ := proto.Marshal(event)
	if e = f.dispatcher.HandleEvent(context.Background(), raw); e != nil {
		t.Fatal(e)
	}
	if e = f.dispatcher.HandleEvent(context.Background(), raw); e != nil {
		t.Fatal("repeat Kafka delivery", e)
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE task_id=?`, task.TaskId).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate consume created duplicate command")
	}
}

type recordingBus struct {
	fail   bool
	events []*commonv1.TaskEvent
	topics []string
}

func (b *recordingBus) Publish(ctx context.Context, topic, key string, raw []byte) error {
	if b.fail {
		return errors.New("injected broker failure")
	}
	if strings.HasSuffix(topic, "task-events.v1") {
		event := &commonv1.TaskEvent{}
		if e := proto.Unmarshal(raw, event); e != nil {
			return e
		}
		b.events = append(b.events, event)
	}
	b.topics = append(b.topics, topic)
	return nil
}
func (b *recordingBus) Consume(ctx context.Context, group, topic string, h func(context.Context, []byte) error) error {
	return errors.New("recording test producer does not consume")
}
func TestA18OutboxOrderFailureAndExpiredLease(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "outbox")
	if _, e := f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE outbox_events SET created_at=UTC_TIMESTAMP()-INTERVAL 8 DAY`); e != nil {
		t.Fatal(e)
	}
	bus := &recordingBus{fail: true}
	if e := f.service.PublishOutbox(context.Background(), bus); e != nil {
		t.Fatal(e)
	}
	var published int
	f.db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE published_at IS NOT NULL`).Scan(&published)
	if published != 0 {
		t.Fatal("failed publication marked published")
	}
	bus.fail = false
	if e := f.service.PublishOutbox(context.Background(), bus); e != nil {
		t.Fatal(e)
	}
	if len(bus.events) != 0 {
		t.Fatal("later task event crossed earlier retry delay")
	}
	f.db.Exec(`UPDATE outbox_events SET next_attempt_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND,lease_owner='crashed',lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE published_at IS NULL`)
	if e := f.service.PublishOutbox(context.Background(), bus); e != nil {
		t.Fatal(e)
	}
	if len(bus.events) != 2 || bus.events[0].StatusVersion != 1 || bus.events[1].StatusVersion != 2 {
		t.Fatal("per-task event ordering or old pending retention failed")
	}
}
// Release all callers after reading the same task so network latency cannot
// accidentally turn the concurrent retry check into ten sequential requests.
type gatedTaskReadClient struct {
	taskv1.TaskServiceClient
	waiting atomic.Int32
	ready   chan struct{}
}

func (c *gatedTaskReadClient) GetTask(ctx context.Context, r *taskv1.GetTaskRequest, options ...grpc.CallOption) (*taskv1.GetTaskResponse, error) {
	response, err := c.TaskServiceClient.GetTask(ctx, r, options...)
	if err != nil {
		return nil, err
	}
	if c.waiting.Add(-1) == 0 {
		close(c.ready)
	}
	select {
	case <-c.ready:
		return response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestA23DurableDLQAndConcurrentManualRetry(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "dlq")
	persistCreated(t, f, task)
	for attempt := 1; attempt <= 5; attempt++ {
		f.db.Exec(`UPDATE task_dispatches SET next_attempt_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE status='pending'`)
		if found, e := f.dispatcher.dispatchOne(context.Background()); !found || e != nil {
			t.Fatal("attempt send", attempt, found, e)
		}
		f.db.Exec(`UPDATE task_dispatches SET next_attempt_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE status='dispatched'`)
		if found, e := f.dispatcher.dispatchOne(context.Background()); !found || e != nil {
			t.Fatal("attempt timeout", attempt, found, e)
		}
	}
	get, e := f.dispatchClient.GetDispatch(f.ctx("tenant", "operator"), &dispatcherv1.GetDispatchRequest{TaskId: task.TaskId})
	if e != nil || get.DeliveryStatus != "dlq" || get.Attempt != 5 {
		t.Fatal("durable DLQ missing", get, e)
	}
	bus := &recordingBus{fail: true}
	if e = f.dispatcher.PublishDLQ(context.Background(), bus); e == nil {
		t.Fatal("DLQ failure hidden")
	}
	var dlq int
	f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE dlq_at IS NOT NULL AND dlq_published_at IS NULL`).Scan(&dlq)
	if dlq != 1 {
		t.Fatal("DLQ fact lost on Kafka failure")
	}
	bus.fail = false
	if e = f.dispatcher.PublishDLQ(context.Background(), bus); e != nil {
		t.Fatal(e)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	originalTaskClient := f.dispatcher.task
	gate := &gatedTaskReadClient{TaskServiceClient: originalTaskClient, ready: make(chan struct{})}
	gate.waiting.Store(10)
	f.dispatcher.task = gate
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := f.dispatchClient.RetryDLQ(f.ctx("tenant", "admin"), &dispatcherv1.RetryDLQRequest{TaskId: task.TaskId, Reason: "operator retry"})
			if e != nil {
				t.Error(e)
				return
			}
			if r.Accepted {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	f.dispatcher.task = originalTaskClient
	if accepted.Load() != 1 {
		t.Fatal("manual retry accepted more than once", accepted.Load())
	}
	var count int
	f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE retry_round=1`).Scan(&count)
	if count != 1 {
		t.Fatal("retry round duplicate")
	}
	f.db.Exec(`UPDATE tasks SET deadline=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE task_id=?`, task.TaskId)
	r, e := f.dispatchClient.RetryDLQ(f.ctx("tenant", "admin"), &dispatcherv1.RetryDLQRequest{TaskId: task.TaskId, Reason: "too late"})
	if e != nil || r.Accepted {
		t.Fatal("expired task retried", e)
	}
}
func TestA22TerminalResultAndLateAudit(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "terminal")
	persistCreated(t, f, task)
	version := int32(1)
	for _, st := range []commonv1.TaskStatus{commonv1.TaskStatus_TASK_STATUS_ACKED, commonv1.TaskStatus_TASK_STATUS_EXECUTING, commonv1.TaskStatus_TASK_STATUS_SUCCEEDED} {
		r := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.TaskId, ExecutorId: "executor", DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: version, Status: st, OccurredAt: timestamppb.Now()}
		if st == commonv1.TaskStatus_TASK_STATUS_SUCCEEDED {
			r.ResultJson = fmt.Sprintf(`{"inspection_id":%q,"entity_id":"drone-001","outcome":"ok","effect_count":1}`, task.TaskId)
		}
		res, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), r)
		if e != nil {
			t.Fatal(e)
		}
		version = res.StatusVersion
	}
	before, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if e != nil {
		t.Fatal(e)
	}
	late := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.TaskId, ExecutorId: "executor", DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: 1, Status: commonv1.TaskStatus_TASK_STATUS_FAILED, Reason: "late transport", OccurredAt: timestamppb.Now()}
	response, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), late)
	if e != nil || response.Disposition != taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_LATE_RESULT {
		t.Fatal("late terminal evidence rejected", e)
	}
	after, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if e != nil || !proto.Equal(before.Task, after.Task) {
		t.Fatal("late report changed terminal fact", e)
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM task_execution_reports WHERE event_id=? AND disposition='late_result'`, late.EventId).Scan(&n)
	if n != 1 {
		t.Fatal("late evidence missing")
	}
	if _, e = f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "after success"}); e != nil {
		t.Fatal(e)
	}
}

// Ensure source-relative fixture paths remain valid when executed by go test.
func TestIntegrationFixtureFiles(t *testing.T) {
	if _, e := os.Stat(filepath.Join("..", "..", "testdata", "migrations", "001_existing_task.sql")); e != nil {
		t.Fatal(e)
	}
}
