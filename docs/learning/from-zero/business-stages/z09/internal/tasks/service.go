package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"slices"
	"time"
	"unicode/utf8"
)

type Service struct {
	taskv1.UnimplementedTaskServiceServer
	cfg        platform.Settings
	reg        *platform.Registry
	db         *sql.DB
	entity     entityv1.EntityServiceClient
	dispatcher dispatcherv1.DispatcherServiceClient
	cursorKey  []byte
}

func NewService(cfg platform.Settings, reg *platform.Registry, db *sql.DB, entity entityv1.EntityServiceClient, dispatcher dispatcherv1.DispatcherServiceClient) (*Service, error) {
	key := []byte(os.Getenv(cfg.CursorKeyEnv))
	if len(key) < 32 {
		return nil, errors.New("CursorKeyEnv must reference at least 32 bytes")
	}
	return &Service{cfg: cfg, reg: reg, db: db, entity: entity, dispatcher: dispatcher, cursorKey: key}, nil
}
func internalContext(ctx context.Context, r *platform.Registry, tenant, role string, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	token, e := r.ServiceToken(tenant, role)
	if e != nil {
		return nil, nil, status.Error(codes.Unavailable, "tenant service identity unavailable")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(platform.Outgoing(ctx, token), timeout)
	return ctx, cancel, nil
}
func validateID(id string, max int) error {
	if !platform.ValidID(id, max) {
		return status.Error(codes.InvalidArgument, "invalid identifier")
	}
	return nil
}
func auth(ctx context.Context, roles ...string) (platform.Principal, error) {
	p := platform.Identity(ctx)
	if p.TenantID == "" {
		return p, status.Error(codes.Unauthenticated, "identity required")
	}
	if !slices.Contains(roles, p.Role) {
		return p, status.Error(codes.PermissionDenied, "role cannot invoke this operation")
	}
	return p, nil
}
func executorOwns(p platform.Principal, t *commonv1.Task) bool {
	return p.Role != "executor" || p.ExecutorID == t.ExecutorId && slices.Contains(p.EntityIDs, t.TargetEntityId)
}
func (s *Service) get(ctx context.Context, tenant, id string) (*commonv1.Task, string, error) {
	t, h, e := readTask(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE tenant_id=? AND task_id=?", tenant, id))
	return t, h, unavailable(e)
}
func (s *Service) CreateTask(ctx context.Context, r *taskv1.CreateTaskRequest) (*taskv1.CreateTaskResponse, error) {
	p, e := auth(ctx, "operator", "admin")
	if e != nil {
		return nil, e
	}
	if e = validateID(r.IdempotencyKey, 256); e != nil {
		return nil, e
	}
	if e = validateID(r.TargetEntityId, 128); e != nil {
		return nil, e
	}
	payload, hash, priority, e := normalizeCreate(r)
	if e != nil {
		return nil, e
	}
	existing := func() (*taskv1.CreateTaskResponse, error) {
		t, h, e := readTask(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE tenant_id=? AND idempotency_key=?", p.TenantID, r.IdempotencyKey))
		if e != nil {
			return nil, e
		}
		if h != hash {
			return nil, status.Error(codes.AlreadyExists, "idempotency key has different normalized parameters")
		}
		return createResponse(t), nil
	}
	if response, e := existing(); !errors.Is(e, sql.ErrNoRows) {
		if e != nil && status.Code(e) == codes.Unknown {
			e = unavailable(e)
		}
		return response, e
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	deadline := now.Add(5 * time.Minute)
	if r.Deadline != nil {
		deadline = r.Deadline.AsTime()
		if !deadline.After(now) || deadline.After(now.Add(24*time.Hour)) {
			return nil, status.Error(codes.InvalidArgument, "deadline must be in the next 24 hours")
		}
	}
	binding, ok := s.reg.Lookup(p.TenantID, r.TargetEntityId)
	if !ok {
		return nil, status.Error(codes.NotFound, "entity not found")
	}
	if binding.ExecutorID == "" || !slices.Contains(binding.Tasks, "inspect") {
		return nil, status.Error(codes.FailedPrecondition, "entity has no registered inspect capability")
	}
	rpc, cancel, e := internalContext(ctx, s.reg, p.TenantID, "task_service", platform.Duration(s.cfg.UnaryTimeout))
	if e != nil {
		return nil, e
	}
	snapshot, e := s.entity.GetSnapshot(rpc, &entityv1.GetSnapshotRequest{EntityId: r.TargetEntityId})
	cancel()
	if e != nil {
		return nil, e
	}
	if !snapshot.Found {
		return nil, status.Error(codes.NotFound, "entity snapshot not found")
	}
	snap := snapshot.Snapshot
	if snap == nil || snapshot.ExpiresAt == nil || !snapshot.ExpiresAt.AsTime().After(now) || snap.GetStatus() == "offline" || snap.GetStatus() == "busy" || snap.GetStatus() == "fault" || snap.GetPerson() != nil && !snap.GetPerson().OnDuty || !slices.Contains(snap.GetCapability().GetSupportedTasks(), "inspect") {
		return nil, status.Error(codes.FailedPrecondition, "entity is stale, unavailable, or lacks inspect capability")
	}
	catalogOK := false
	for _, d := range snap.GetTaskCatalog().GetDefinitions() {
		if d.TaskType == "inspect" {
			catalogOK = true
		}
	}
	if !catalogOK || snapshot.ExecutorId != binding.ExecutorID {
		return nil, status.Error(codes.FailedPrecondition, "catalog or executor binding mismatch")
	}
	t := &commonv1.Task{TaskId: platform.NewID(), TenantId: p.TenantID, IdempotencyKey: r.IdempotencyKey, TaskType: r.TaskType, TargetEntityId: r.TargetEntityId, Payload: &commonv1.TaskPayload{PayloadJson: payload}, Priority: priority, Status: commonv1.TaskStatus_TASK_STATUS_CREATED, CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now), Deadline: timestamppb.New(deadline), CreatedBy: p.ID, ExecutorId: binding.ExecutorID}
	t.ExecutionKey = t.TaskId
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, unavailable(e)
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `INSERT INTO tasks(tenant_id,task_id,idempotency_key,task_type,target_entity_id,payload,priority,status,status_version,created_at,updated_at,deadline,created_by,executor_id,execution_key,request_hash) VALUES(?,?,?,?,?,?,?,'CREATED',0,?,?,?,?,?,?,?)`, t.TenantId, t.TaskId, t.IdempotencyKey, t.TaskType, t.TargetEntityId, payload, priority, now, now, deadline, p.ID, t.ExecutorId, t.ExecutionKey, hash)
	if duplicate(e) {
		tx.Rollback()
		response, err := existing()
		if err != nil && status.Code(err) == codes.Unknown {
			err = unavailable(err)
		}
		return response, err
	}
	if e != nil {
		return nil, unavailable(e)
	}
	// 两条创建审计共享摘要用于关联；不再把客户端原始幂等键复制到 reason。
	// Task 仍保留原键以维持幂等查询，这里仅减少审计文本的重复留存。
	creationReason := "create request sha256:" + digest([]byte(r.IdempotencyKey))
	if e = writeAudit(ctx, tx, t, commonv1.TaskStatus_TASK_STATUS_UNSPECIFIED, p.ID, creationReason, platform.NewID()); e != nil {
		return nil, unavailable(e)
	}
	t.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
	t.StatusVersion = 1
	if e = persistChange(ctx, tx, t, commonv1.TaskStatus_TASK_STATUS_CREATED, p.ID, creationReason, platform.NewID(), commonv1.TaskEventType_TASK_EVENT_TYPE_CREATED); e != nil {
		return nil, unavailable(e)
	}
	if e = tx.Commit(); e != nil {
		return nil, unavailable(e)
	}
	return createResponse(t), nil
}
func createResponse(t *commonv1.Task) *taskv1.CreateTaskResponse {
	return &taskv1.CreateTaskResponse{TaskId: t.TaskId, Status: t.Status, CreatedAt: t.CreatedAt}
}
func (s *Service) GetTask(ctx context.Context, r *taskv1.GetTaskRequest) (*taskv1.GetTaskResponse, error) {
	p, e := auth(ctx, "operator", "admin", "executor", "dispatcher_service")
	if e != nil {
		return nil, e
	}
	if e = validateID(r.TaskId, 128); e != nil {
		return nil, e
	}
	t, _, e := s.get(ctx, p.TenantID, r.TaskId)
	if e != nil {
		return nil, e
	}
	if !executorOwns(p, t) {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	return &taskv1.GetTaskResponse{Task: t}, nil
}
func (s *Service) CancelTask(ctx context.Context, r *taskv1.CancelTaskRequest) (*taskv1.CancelTaskResponse, error) {
	p, e := auth(ctx, "operator", "admin")
	if e != nil {
		return nil, e
	}
	if e = validateID(r.TaskId, 128); e != nil {
		return nil, e
	}
	if r.Reason == "" || len(r.Reason) > 1024 || !utf8.ValidString(r.Reason) {
		return nil, status.Error(codes.InvalidArgument, "reason must be 1..1024 UTF-8 bytes")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, unavailable(e)
	}
	defer tx.Rollback()
	t, _, e := readTask(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE tenant_id=? AND task_id=? FOR UPDATE", p.TenantID, r.TaskId))
	if e != nil {
		return nil, unavailable(e)
	}
	// 取消是只推进一次的请求标记，行锁与回报/超时共用；无需客户端提供旧版本。
	// 重复请求保留第一次 reason，已提交的终态则直接返回当前执行事实。
	if !Terminal(t.Status) && !t.CancelRequested {
		t.CancelRequested = true
		t.CancelledReason = r.Reason
		t.StatusVersion++
		t.UpdatedAt = timestamppb.Now()
		if e = persistChange(ctx, tx, t, t.Status, p.ID, r.Reason, platform.NewID(), commonv1.TaskEventType_TASK_EVENT_TYPE_CANCEL_REQUESTED); e != nil {
			return nil, unavailable(e)
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, unavailable(e)
	}
	return &taskv1.CancelTaskResponse{Success: !Terminal(t.Status) || t.Status == commonv1.TaskStatus_TASK_STATUS_CANCELLED, CurrentStatus: t.Status, CancelRequested: t.CancelRequested, Message: "current_status is the execution fact; cancellation request alone does not prove stopped"}, nil
}
func (s *Service) ReportTaskStatus(ctx context.Context, r *taskv1.ReportTaskStatusRequest) (*taskv1.ReportTaskStatusResponse, error) {
	p, e := auth(ctx, "executor", "dispatcher_service")
	if e != nil {
		return nil, e
	}
	for _, v := range []struct {
		id  string
		max int
	}{{r.TaskId, 128}, {r.EventId, 128}, {r.ExecutionKey, 128}, {r.ExecutorId, 128}, {r.DispatchId, 256}} {
		if e = validateID(v.id, v.max); e != nil {
			return nil, e
		}
	}
	if r.OccurredAt == nil || r.OccurredAt.CheckValid() != nil || len(r.ResultJson) > 65536 || len(r.Reason) > 1024 || !utf8.ValidString(r.Reason) || r.ExpectedStatusVersion < 1 {
		return nil, status.Error(codes.InvalidArgument, "invalid report fields")
	}
	if p.Role == "dispatcher_service" && r.Status != commonv1.TaskStatus_TASK_STATUS_DISPATCHED {
		return nil, status.Error(codes.PermissionDenied, "dispatcher can only report DISPATCHED")
	}
	encoded, _ := proto.MarshalOptions{Deterministic: true}.Marshal(r)
	hash := digest(encoded)
	// Bindings are immutable during service lifetime. Verify the remote dispatch
	// before acquiring the task lock, then recheck receipt/version under the lock.
	// The first duplicate lookup preserves successful retries during RPC outages.
	pre, _, e := s.get(ctx, p.TenantID, r.TaskId)
	if e != nil {
		return nil, e
	}
	if !executorOwns(p, pre) || pre.ExecutorId != r.ExecutorId || pre.ExecutionKey != r.ExecutionKey {
		return nil, status.Error(codes.PermissionDenied, "executor or execution binding mismatch")
	}
	var receiptHash string
	e = s.db.QueryRowContext(ctx, `SELECT request_hash FROM task_execution_reports WHERE tenant_id=? AND event_id=?`, p.TenantID, r.EventId).Scan(&receiptHash)
	if e == nil {
		if receiptHash != hash {
			return nil, status.Error(codes.AlreadyExists, "report event_id content conflict")
		}
		return reportResponse(pre, taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_DUPLICATE), nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, unavailable(e)
	}
	rpc, cancel, e := internalContext(ctx, s.reg, p.TenantID, "task_service", platform.Duration(s.cfg.UnaryTimeout))
	if e != nil {
		return nil, e
	}
	d, e := s.dispatcher.GetDispatch(rpc, &dispatcherv1.GetDispatchRequest{TaskId: r.TaskId, DispatchId: r.DispatchId})
	cancel()
	if e != nil {
		return nil, e
	}
	kind := "execute"
	if r.Status == commonv1.TaskStatus_TASK_STATUS_CANCELLED {
		kind = "cancel"
	}
	if d.TaskId != pre.TaskId || d.DispatchId != r.DispatchId || d.ExecutorId != pre.ExecutorId || d.ExecutionKey != pre.ExecutionKey || d.CommandKind != kind {
		return nil, status.Error(codes.PermissionDenied, "referenced dispatch binding or kind mismatch")
	}
	// pending 仅说明命令已入队，不能授权执行回报。dispatched_at 是发送前落盘的
	// 持久意图，不证明已收到字节；旧 timeout/DLQ 回报仍可凭这项历史事实验权。
	if d.DispatchedAt == nil || d.DispatchedAt.CheckValid() != nil {
		return nil, status.Error(codes.FailedPrecondition, "referenced dispatch has no durable send intent")
	}
	// Task row serializes receipts, transitions, cancellation and timeout. No accepted receipt survives rollback.
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, unavailable(e)
	}
	defer tx.Rollback()
	t, _, e := readTask(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE tenant_id=? AND task_id=? FOR UPDATE", p.TenantID, r.TaskId))
	if e != nil {
		return nil, unavailable(e)
	}
	if !executorOwns(p, t) || t.ExecutorId != r.ExecutorId || t.ExecutionKey != r.ExecutionKey {
		return nil, status.Error(codes.PermissionDenied, "executor or execution binding mismatch")
	}
	var oldHash string
	e = tx.QueryRowContext(ctx, `SELECT request_hash FROM task_execution_reports WHERE tenant_id=? AND event_id=?`, p.TenantID, r.EventId).Scan(&oldHash)
	if e == nil {
		if oldHash != hash {
			return nil, status.Error(codes.AlreadyExists, "report event_id content conflict")
		}
		return reportResponse(t, taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_DUPLICATE), nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, unavailable(e)
	}
	if r.Status == commonv1.TaskStatus_TASK_STATUS_SUCCEEDED {
		expected, _ := json.Marshal(map[string]any{"inspection_id": t.TaskId, "entity_id": t.TargetEntityId, "outcome": "ok", "effect_count": 1})
		var got, want any
		if json.Unmarshal([]byte(r.ResultJson), &got) != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid success result")
		}
		json.Unmarshal(expected, &want)
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if string(gb) != string(wb) {
			return nil, status.Error(codes.InvalidArgument, "success result must match deterministic inspect contract")
		}
	} else if r.ResultJson != "" {
		return nil, status.Error(codes.InvalidArgument, "only SUCCEEDED carries result_json")
	}
	late := Terminal(t.Status)
	if late && !Terminal(r.Status) {
		return nil, status.Error(codes.FailedPrecondition, "task is terminal")
	}
	if !late {
		if r.ExpectedStatusVersion != t.StatusVersion {
			return nil, status.Error(codes.Aborted, "status_version changed")
		}
		if !CanTransition(t.Status, r.Status, t.CancelRequested, p.Role) {
			return nil, status.Error(codes.FailedPrecondition, "illegal task transition")
		}
	}
	disposition := "applied"
	responseDisposition := taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_APPLIED
	var applied any = t.StatusVersion + 1
	if late {
		disposition = "late_result"
		responseDisposition = taskv1.StatusReportDisposition_STATUS_REPORT_DISPOSITION_LATE_RESULT
		applied = nil
	}
	var result any
	if r.ResultJson != "" {
		result = r.ResultJson
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO task_execution_reports(tenant_id,event_id,task_id,executor_id,execution_key,dispatch_id,request_hash,expected_status_version,reported_status,result,reason,occurred_at,disposition,applied_status_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.TenantID, r.EventId, t.TaskId, r.ExecutorId, r.ExecutionKey, r.DispatchId, hash, r.ExpectedStatusVersion, statusName(r.Status), result, r.Reason, r.OccurredAt.AsTime(), disposition, applied)
	if e != nil {
		return nil, unavailable(e)
	}
	if !late {
		from := t.Status
		t.Status = r.Status
		t.StatusVersion++
		t.UpdatedAt = timestamppb.Now()
		if Terminal(t.Status) {
			t.CompletedAt = t.UpdatedAt
			t.ResultJson = r.ResultJson
			if t.Status != commonv1.TaskStatus_TASK_STATUS_SUCCEEDED {
				t.FailureReason = r.Reason
			}
		}
		if e = persistChange(ctx, tx, t, from, p.ID, r.Reason, r.EventId, eventType(t.Status)); e != nil {
			return nil, unavailable(e)
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, unavailable(e)
	}
	return reportResponse(t, responseDisposition), nil
}
func reportResponse(t *commonv1.Task, d taskv1.StatusReportDisposition) *taskv1.ReportTaskStatusResponse {
	return &taskv1.ReportTaskStatusResponse{CurrentStatus: t.Status, StatusVersion: t.StatusVersion, Disposition: d}
}
