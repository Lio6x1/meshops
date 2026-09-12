//go:build integration

package tasks

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

func TestA17ListStableTimestampCursorAndTenantBinding(t *testing.T) {
	f := fixtureFor(t)
	for i := 0; i < 5; i++ {
		create(t, f, fmt.Sprintf("page-%d", i))
	}
	if _, e := f.db.Exec(`UPDATE tasks SET created_at='2026-09-01 00:00:00.123456'`); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	pageToken := ""
	for page := 0; page < 3; page++ {
		r, e := f.client.ListTasks(f.ctx("tenant", "operator"), &taskv1.ListTasksRequest{PageSize: 2, PageToken: pageToken})
		if e != nil {
			t.Fatal(e)
		}
		if r.TotalCount != 5 {
			t.Fatal("inconsistent upper-bound count")
		}
		for _, task := range r.Tasks {
			if seen[task.TaskId] {
				t.Fatal("duplicate task across equal-timestamp page")
			}
			seen[task.TaskId] = true
		}
		if page == 0 {
			for _, x := range []struct{ tenant, entity, token string }{{"other", "", r.NextPageToken}, {"tenant", "other-entity", r.NextPageToken}, {"tenant", "", r.NextPageToken + "x"}} {
				if _, e = f.client.ListTasks(f.ctx(x.tenant, "operator"), &taskv1.ListTasksRequest{PageSize: 2, PageToken: x.token, TargetEntityId: x.entity}); status.Code(e) != codes.InvalidArgument {
					t.Fatal("cursor tenant/filter/signature not enforced", e)
				}
			}
		}
		pageToken = r.NextPageToken
	}
	if len(seen) != 5 || pageToken != "" {
		t.Fatal("pagination omitted task or failed to terminate")
	}
}

func TestStateOnlyEntitiesRejectInspectWithoutPersistingTask(t *testing.T) {
	f := fixtureFor(t)
	for _, kind := range []string{"sensor", "facility"} {
		id, source := kind+"-001", kind+"_source"
		f.reg.Bindings["tenant:"+id] = platform.Binding{TenantID: "tenant", EntityID: id, Type: kind, SourceID: source, SourceGeneration: 1}
		f.reg.Sources["tenant:"+source] = platform.Source{TenantID: "tenant", ID: source, Adapter: kind, Generation: 1, StaleAfter: 30 * time.Second, Entities: map[string]string{id: id}}
		token := fmt.Sprintf("isolated-state-source-credential-%s", kind)
		f.reg.Credentials["tenant:"+source] = token
		f.reg.Principals[platform.Hash([]byte(token))] = platform.Principal{ID: source, Role: "source", TenantID: "tenant", SourceID: source}
	}
	if err := Seed(context.Background(), f.db, f.reg); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"sensor", "facility"} {
		r := request("reject-" + kind)
		r.TargetEntityId = kind + "-001"
		if _, err := f.client.CreateTask(f.ctx("tenant", "operator"), r); status.Code(err) != codes.FailedPrecondition {
			t.Fatal("state-only entity admitted inspect", kind, err)
		}
	}
	for _, table := range []string{"tasks", "task_status_history", "outbox_events"} {
		var count int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("rejected inspect persisted partial facts", table, count, err)
		}
	}
}
func TestA18PublishThenMarkFailureReplaysStableEvent(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "publish-crash")
	if _, e := f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: "ordered"}); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`CREATE TRIGGER fail_published BEFORE UPDATE ON outbox_events FOR EACH ROW BEGIN IF NEW.published_at IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='mark crash'; END IF; END`); e != nil {
		t.Fatal(e)
	}
	bus := &recordingBus{}
	if _, e := f.service.publishOne(context.Background(), bus); e == nil {
		t.Fatal("post-publish persistence failure hidden")
	}
	if len(bus.events) != 1 {
		t.Fatal("event was not actually delivered to producer")
	}
	if _, e := f.db.Exec(`DROP TRIGGER fail_published`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.db.Exec(`UPDATE outbox_events SET lease_until=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE lease_owner IS NOT NULL`); e != nil {
		t.Fatal(e)
	}
	if e := f.service.PublishOutbox(context.Background(), bus); e != nil {
		t.Fatal(e)
	}
	if len(bus.events) != 3 || bus.events[0].EventId != bus.events[1].EventId || bus.events[2].StatusVersion != 2 {
		t.Fatal("ambiguous publish did not replay stable ID in order")
	}
}
func TestA19AuthenticatedSingleExecutorStreamAndDurableDelivery(t *testing.T) {
	f := fixtureFor(t)
	ctx, cancel := context.WithCancel(f.ctx("tenant", "executor"))
	defer cancel()
	stream, e := f.executorClient.ListenTasks(ctx, &executorv1.ListenTasksRequest{ExecutorId: "executor"})
	if e != nil {
		t.Fatal(e)
	}
	wait := time.Now().Add(3 * time.Second)
	for {
		f.dispatcher.mu.Lock()
		active := f.dispatcher.streams["tenant:executor"] != nil
		f.dispatcher.mu.Unlock()
		if active {
			break
		}
		if time.Now().After(wait) {
			t.Fatal("executor stream not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	duplicate, e := f.executorClient.ListenTasks(f.ctx("tenant", "executor"), &executorv1.ListenTasksRequest{ExecutorId: "executor"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = duplicate.Recv(); status.Code(e) != codes.ResourceExhausted {
		t.Fatal("second executor stream admitted", e)
	}
	wrong, e := f.executorClient.ListenTasks(f.ctx("tenant", "executor"), &executorv1.ListenTasksRequest{ExecutorId: "someone-else"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wrong.Recv(); status.Code(e) != codes.PermissionDenied {
		t.Fatal("wrong executor stream admitted", e)
	}
	task := create(t, f, "live-stream")
	persistCreated(t, f, task)
	received := make(chan *executorv1.ListenTasksResponse, 1)
	errs := make(chan error, 1)
	go func() {
		r, e := stream.Recv()
		if e != nil {
			errs <- e
			return
		}
		received <- r
	}()
	if found, e := f.dispatcher.dispatchOne(context.Background()); !found || e != nil {
		t.Fatal("durable delivery", found, e)
	}
	select {
	case e := <-errs:
		t.Fatal(e)
	case r := <-received:
		if r.Task.TaskId != task.TaskId || r.DispatchId != task.TaskId+"-execute-1" || r.Attempt != 1 {
			t.Fatal("incorrect delivery identity")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command never arrived")
	}
	get, e := f.dispatchClient.GetDispatch(f.ctx("tenant", "operator"), &dispatcherv1.GetDispatchRequest{TaskId: task.TaskId})
	if e != nil || get.DeliveryStatus != "dispatched" {
		t.Fatal("send not backed by durable attempt", e)
	}
	cancel()
	wait = time.Now().Add(3 * time.Second)
	for {
		f.dispatcher.mu.Lock()
		active := f.dispatcher.streams["tenant:executor"] != nil
		f.dispatcher.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(wait) {
			t.Fatal("cancelled stream leaked registry slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func emitTaskEvent(t *testing.T, f *fixture, task *commonv1.Task, kind commonv1.TaskEventType) {
	t.Helper()
	data, _ := protojson.Marshal(task)
	event := &commonv1.TaskEvent{EventId: platform.NewID(), TenantId: task.TenantId, TaskId: task.TaskId, CurrentStatus: task.Status, StatusVersion: task.StatusVersion, DataJson: string(data), EventType: kind}
	raw, _ := proto.Marshal(event)
	if e := f.dispatcher.HandleEvent(context.Background(), raw); e != nil {
		t.Fatal(e)
	}
}
func TestA22CancellationSuccessTimeoutBarrierAndOldEvent(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "race")
	original := proto.Clone(task).(*commonv1.Task)
	persistCreated(t, f, task)
	persistDispatchIntent(t, f)
	version := int32(1)
	for _, st := range []commonv1.TaskStatus{commonv1.TaskStatus_TASK_STATUS_ACKED, commonv1.TaskStatus_TASK_STATUS_EXECUTING} {
		r, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.TaskId, ExecutorId: "executor", DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: version, Status: st, OccurredAt: timestamppb.Now()})
		if e != nil {
			t.Fatal(e)
		}
		version = r.StatusVersion
	}
	for i := 0; i < 2; i++ {
		if _, e := f.client.CancelTask(f.ctx("tenant", "operator"), &taskv1.CancelTaskRequest{TaskId: task.TaskId, Reason: fmt.Sprintf("cancel-%d", i)}); e != nil {
			t.Fatal(e)
		}
	}
	current, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if e != nil {
		t.Fatal(e)
	}
	if current.Task.StatusVersion != 4 || current.Task.CancelledReason != "cancel-0" {
		t.Fatal("repeated cancel modified version/reason")
	}
	emitTaskEvent(t, f, current.Task, commonv1.TaskEventType_TASK_EVENT_TYPE_CANCEL_REQUESTED)
	persistDispatchIntent(t, f)
	wrong := &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: task.TaskId, ExecutionKey: task.TaskId, ExecutorId: "executor", DispatchId: task.TaskId + "-execute-1", ExpectedStatusVersion: 4, Status: commonv1.TaskStatus_TASK_STATUS_CANCELLED, OccurredAt: timestamppb.Now()}
	if _, e = f.client.ReportTaskStatus(f.ctx("tenant", "executor"), wrong); status.Code(e) != codes.PermissionDenied {
		t.Fatal("CANCELLED accepted execute dispatch", e)
	}
	cancelReport := proto.Clone(wrong).(*taskv1.ReportTaskStatusRequest)
	cancelReport.EventId = platform.NewID()
	cancelReport.DispatchId = task.TaskId + "-cancel-1"
	successReport := proto.Clone(wrong).(*taskv1.ReportTaskStatusRequest)
	successReport.EventId = platform.NewID()
	successReport.Status = commonv1.TaskStatus_TASK_STATUS_SUCCEEDED
	successReport.ResultJson = fmt.Sprintf(`{"inspection_id":%q,"entity_id":"drone-001","outcome":"ok","effect_count":1}`, task.TaskId)
	if _, e = f.db.Exec(`UPDATE tasks SET deadline=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE task_id=?`, task.TaskId); e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	done := make(chan error, 3)
	for _, r := range []*taskv1.ReportTaskStatusRequest{cancelReport, successReport} {
		go func(r *taskv1.ReportTaskStatusRequest) {
			<-start
			_, e := f.client.ReportTaskStatus(f.ctx("tenant", "executor"), r)
			done <- e
		}(r)
	}
	go func() { <-start; done <- f.service.ExpireTasks(context.Background()) }()
	close(start)
	for i := 0; i < 3; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	final, e := f.client.GetTask(f.ctx("tenant", "operator"), &taskv1.GetTaskRequest{TaskId: task.TaskId})
	if e != nil {
		t.Fatal(e)
	}
	if !Terminal(final.Task.Status) || final.Task.StatusVersion != 5 || final.Task.CompletedAt == nil {
		t.Fatal("terminal barrier changed fact more than once")
	}
	if final.Task.Status != commonv1.TaskStatus_TASK_STATUS_SUCCEEDED && final.Task.ResultJson != "" {
		t.Fatal("losing success result overwrote terminal result")
	}
	var n int
	f.db.QueryRow(`SELECT COUNT(*) FROM task_status_history WHERE task_id=? AND to_status IN ('SUCCEEDED','CANCELLED','TIMED_OUT')`, task.TaskId).Scan(&n)
	if n != 1 {
		t.Fatal("multiple terminal audits committed")
	}
	emitTaskEvent(t, f, final.Task, eventType(final.Task.Status))
	emitTaskEvent(t, f, original, commonv1.TaskEventType_TASK_EVENT_TYPE_CREATED)
	get, e := f.dispatchClient.GetDispatch(f.ctx("tenant", "operator"), &dispatcherv1.GetDispatchRequest{TaskId: task.TaskId})
	if e != nil || get.Status != final.Task.Status {
		t.Fatal("old created event regressed terminal mirror", e)
	}
	f.db.QueryRow(`SELECT COUNT(*) FROM task_dispatches WHERE task_id=?`, task.TaskId).Scan(&n)
	if n != 2 {
		t.Fatal("old event created extra command after terminal")
	}
}
