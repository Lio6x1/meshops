package tasks

import (
	"context"
	"database/sql"
	"errors"
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
	"log/slog"
	"slices"
	"sync"
	"time"
)

type Dispatcher struct {
	dispatcherv1.UnimplementedDispatcherServiceServer
	executorv1.UnimplementedExecutorServiceServer
	cfg       platform.Settings
	reg       *platform.Registry
	db        *sql.DB
	task      taskv1.TaskServiceClient
	lagReader func(context.Context, string, string) (int64, error)
	mu        sync.Mutex
	streams   map[string]*connection
}
type queuedCommand struct {
	command *executorv1.ListenTasksResponse
	size    int
	sent    chan error
}
type connection struct {
	queue    chan queuedCommand
	done     chan struct{}
	bytes    int
	pending  map[string]bool
	entities []string
}

func NewDispatcher(cfg platform.Settings, reg *platform.Registry, db *sql.DB, task taskv1.TaskServiceClient) (*Dispatcher, error) {
	if cfg.MaxAttempts < 1 || len(cfg.RetryDelays) < cfg.MaxAttempts-1 {
		return nil, errors.New("MaxAttempts and RetryDelays are inconsistent")
	}
	return &Dispatcher{cfg: cfg, reg: reg, db: db, task: task, streams: map[string]*connection{}}, nil
}

// SetLagReader 在处理请求或启动 Run 前配置从 broker 读取状态的依赖，
// 使用与消费者相同的消费组和主题。
func (d *Dispatcher) SetLagReader(reader func(context.Context, string, string) (int64, error)) {
	d.lagReader = reader
}
func (d *Dispatcher) ListenTasks(r *executorv1.ListenTasksRequest, stream executorv1.ExecutorService_ListenTasksServer) error {
	p, e := auth(stream.Context(), "executor")
	if e != nil {
		return e
	}
	if r.ExecutorId != p.ExecutorID {
		return status.Error(codes.PermissionDenied, "executor identity mismatch")
	}
	key := platform.Key(p.TenantID, p.ExecutorID)
	c := &connection{queue: make(chan queuedCommand, 100), done: make(chan struct{}), pending: map[string]bool{}, entities: append([]string(nil), p.EntityIDs...)}
	d.mu.Lock()
	if d.streams[key] != nil {
		d.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "executor already has an active stream")
	}
	d.streams[key] = c
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.streams, key); close(c.done); d.mu.Unlock() }()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case q := <-c.queue:
			// 超时后退出处理函数会取消 gRPC 传输，使阻塞的 Send 返回。
			sent := make(chan error, 1)
			go func() { sent <- stream.Send(q.command) }()
			timer := time.NewTimer(5 * time.Second)
			select {
			case e = <-sent:
				timer.Stop()
			case <-timer.C:
				e = status.Error(codes.ResourceExhausted, "executor stream send exceeded five seconds")
			case <-stream.Context().Done():
				timer.Stop()
				e = stream.Context().Err()
			}
			d.mu.Lock()
			c.bytes -= q.size
			delete(c.pending, q.command.CommandId)
			d.mu.Unlock()
			q.sent <- e
			if e != nil {
				return e
			}
		}
	}
}
func (d *Dispatcher) send(ctx context.Context, tenant, executor string, command *executorv1.ListenTasksResponse) error {
	key := platform.Key(tenant, executor)
	size := proto.Size(command)
	q := queuedCommand{command: command, size: size, sent: make(chan error, 1)}
	d.mu.Lock()
	c := d.streams[key]
	if c == nil {
		d.mu.Unlock()
		return errors.New("executor disconnected")
	}
	if !slices.Contains(c.entities, command.GetTask().GetTargetEntityId()) {
		d.mu.Unlock()
		return status.Error(codes.PermissionDenied, "executor entity scope mismatch")
	}
	if c.pending[command.CommandId] {
		d.mu.Unlock()
		return errors.New("command already queued on executor stream")
	}
	if len(c.queue) >= 100 || c.bytes+size > 8*1024*1024 {
		d.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "executor queue capacity reached")
	}
	c.bytes += size
	c.pending[command.CommandId] = true
	c.queue <- q
	d.mu.Unlock()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case e := <-q.sent:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("executor stream closed")
	case <-timer.C:
		return errors.New("executor delivery timed out")
	}
}
func (d *Dispatcher) currentTask(ctx context.Context, tenant, id string) (*commonv1.Task, error) {
	rpc, cancel, e := internalContext(ctx, d.reg, tenant, "dispatcher_service", platform.Duration(d.cfg.UnaryTimeout))
	if e != nil {
		return nil, e
	}
	defer cancel()
	r, e := d.task.GetTask(rpc, &taskv1.GetTaskRequest{TaskId: id})
	if e != nil {
		return nil, e
	}
	if r.Task == nil || r.Task.TenantId != tenant {
		return nil, errors.New("task response identity mismatch")
	}
	return r.Task, nil
}
func (d *Dispatcher) Run(ctx context.Context, b Bus) error {
	ctx, cancel := context.WithCancel(ctx)
	consumed := make(chan error, 1)
	consumerFinished := false
	defer func() {
		cancel()
		if !consumerFinished {
			<-consumed
		}
	}()
	go func() {
		consumed <- b.Consume(ctx, d.cfg.ConsumerGroupPrefix+"dispatcher", d.cfg.TopicPrefix+"task-events.v1", d.HandleEvent)
	}()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-consumed:
			consumerFinished = true
			return e
		case <-ticker.C:
			if e := d.DispatchDue(ctx); e != nil {
				slog.Error("dispatch retry", "error", e)
			}
			if e := d.PublishDLQ(ctx, b); e != nil {
				slog.Error("DLQ notification retained", "error", e)
			}
		}
	}
}
func (d *Dispatcher) HandleEvent(ctx context.Context, raw []byte) error {
	event := &commonv1.TaskEvent{}
	if e := proto.Unmarshal(raw, event); e != nil {
		slog.Warn("quarantined malformed task event")
		return nil
	}
	t := &commonv1.Task{}
	if e := protojson.Unmarshal([]byte(event.DataJson), t); e != nil || event.TaskId != t.TaskId || event.TenantId != t.TenantId || event.StatusVersion != t.StatusVersion || event.CurrentStatus != t.Status || event.EventId == "" || validateID(t.TenantId, 64) != nil || validateID(t.TaskId, 128) != nil || t.StatusVersion < 1 {
		slog.Warn("quarantined inconsistent task event")
		return nil
	}
	binding, ok := d.reg.Lookup(t.TenantId, t.TargetEntityId)
	if !ok || binding.ExecutorID != t.ExecutorId || t.ExecutionKey != t.TaskId {
		slog.Warn("quarantined unregistered task event")
		return nil
	}
	create := event.EventType == commonv1.TaskEventType_TASK_EVENT_TYPE_CREATED || event.EventType == commonv1.TaskEventType_TASK_EVENT_TYPE_CANCEL_REQUESTED
	if create {
		current, e := d.currentTask(ctx, t.TenantId, t.TaskId)
		if e != nil {
			return e
		}
		t = current
	}
	tx, e := d.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// 更新每次尝试，使 GetDispatch 状态保持最新，同时保留历史
	// timeout/DLQ 投递事实。只有更高的状态版本才能更新记录。
	if e = mirrorTask(ctx, tx, t); e != nil {
		return e
	}
	if create && !Terminal(t.Status) {
		kind := "execute"
		if event.EventType == commonv1.TaskEventType_TASK_EVENT_TYPE_CANCEL_REQUESTED {
			kind = "cancel"
		}
		eligible := kind == "cancel" && t.CancelRequested || kind == "execute" && !t.CancelRequested && (t.Status == commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING || t.Status == commonv1.TaskStatus_TASK_STATUS_DISPATCHED)
		if eligible {
			if e = insertAttempt(ctx, tx, t, kind, 1, 0, time.Now().UTC()); e != nil && !duplicate(e) {
				return e
			}
		}
	}
	return tx.Commit()
}
func mirrorTask(ctx context.Context, tx *sql.Tx, t *commonv1.Task) error {
	// Task 状态是业务事实，status 是每次投递的事实，两者不能互相替代。
	// 只接收更高版本，且保留历史 timeout/DLQ/abandoned，避免旧消息复活尝试。
	_, e := tx.ExecContext(ctx, `UPDATE task_dispatches SET
 status=CASE WHEN status IN ('timeout','dlq','abandoned') THEN status
 WHEN ? IN (6,7,8,9,10) THEN CASE WHEN command_kind='execute' AND ?=6 THEN 'succeeded' WHEN command_kind='execute' AND ? IN (7,10) THEN 'failed' WHEN command_kind='cancel' AND ?=8 THEN 'cancelled' ELSE 'abandoned' END
 WHEN command_kind='execute' AND ?=5 THEN 'executing' WHEN command_kind='execute' AND ?=4 THEN 'acked' ELSE status END,
 acked_at=CASE WHEN command_kind='execute' AND ? IN (4,5,6,7) THEN COALESCE(acked_at,UTC_TIMESTAMP(6)) ELSE acked_at END,
 completed_at=CASE WHEN ? IN (6,7,8,9,10) AND status NOT IN ('timeout','dlq') THEN COALESCE(completed_at,UTC_TIMESTAMP(6)) ELSE completed_at END,
 last_task_status=?,last_task_status_version=? WHERE tenant_id=? AND task_id=? AND last_task_status_version<?`, t.Status, t.Status, t.Status, t.Status, t.Status, t.Status, t.Status, t.Status, t.Status, t.StatusVersion, t.TenantId, t.TaskId, t.StatusVersion)
	return e
}
func insertAttempt(ctx context.Context, tx *sql.Tx, t *commonv1.Task, kind string, attempt, round int, due time.Time) error {
	commandID := t.TaskId + "-" + kind
	dispatchID := fmt.Sprintf("%s-%d", commandID, attempt)
	command := &executorv1.ListenTasksResponse{CommandId: commandID, Task: t, DispatchId: dispatchID, Attempt: int32(attempt), Kind: executorv1.TaskCommandKind_TASK_COMMAND_KIND_EXECUTE}
	if kind == "cancel" {
		command.Kind = executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL
	}
	payload, e := protojson.Marshal(command)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO task_dispatches(tenant_id,task_id,dispatch_id,attempt,executor_id,status,execution_key,command_id,command_kind,next_attempt_at,last_task_status,last_task_status_version,retry_round,command_payload) VALUES(?,?,?,?,?,'pending',?,?,?,?,?,?,?,?)`, t.TenantId, t.TaskId, dispatchID, attempt, t.ExecutorId, t.ExecutionKey, commandID, kind, due, t.Status, t.StatusVersion, round, payload)
	return e
}

type attempt struct {
	id                                                           int64
	tenant, task, idString, executor, key, command, kind, status string
	number, round                                                int
	lastStatus                                                   commonv1.TaskStatus
	version                                                      int32
	payload                                                      []byte
	dispatched, acked, completed                                 sql.NullTime
}

const dispatchColumns = `id,COALESCE(tenant_id,''),task_id,dispatch_id,COALESCE(executor_id,''),COALESCE(execution_key,''),COALESCE(command_id,''),command_kind,status,attempt,retry_round,last_task_status,last_task_status_version,COALESCE(command_payload,''),dispatched_at,acked_at,completed_at`

func scanAttempt(s scanner) (attempt, error) {
	var a attempt
	e := s.Scan(&a.id, &a.tenant, &a.task, &a.idString, &a.executor, &a.key, &a.command, &a.kind, &a.status, &a.number, &a.round, &a.lastStatus, &a.version, &a.payload, &a.dispatched, &a.acked, &a.completed)
	return a, e
}
func (d *Dispatcher) GetDispatch(ctx context.Context, r *dispatcherv1.GetDispatchRequest) (*dispatcherv1.GetDispatchResponse, error) {
	p, e := auth(ctx, "operator", "admin", "task_service")
	if e != nil {
		return nil, e
	}
	if e = validateID(r.TaskId, 128); e != nil {
		return nil, e
	}
	query := "SELECT " + dispatchColumns + " FROM task_dispatches WHERE tenant_id=? AND task_id=?"
	args := []any{p.TenantID, r.TaskId}
	if r.DispatchId != "" {
		if e = validateID(r.DispatchId, 256); e != nil {
			return nil, e
		}
		query += " AND dispatch_id=?"
		args = append(args, r.DispatchId)
	}
	query += " ORDER BY id DESC LIMIT 1"
	a, e := scanAttempt(d.db.QueryRowContext(ctx, query, args...))
	if e != nil {
		return nil, unavailable(e)
	}
	out := &dispatcherv1.GetDispatchResponse{TaskId: a.task, DispatchId: a.idString, Attempt: int32(a.number), Status: a.lastStatus, ExecutorId: a.executor, CommandId: a.command, ExecutionKey: a.key, DeliveryStatus: a.status, CommandKind: a.kind}
	if a.dispatched.Valid {
		out.DispatchedAt = timestamppb.New(a.dispatched.Time)
	}
	if a.acked.Valid {
		out.AckedAt = timestamppb.New(a.acked.Time)
	}
	if a.completed.Valid {
		out.CompletedAt = timestamppb.New(a.completed.Time)
	}
	return out, nil
}
func (d *Dispatcher) GetStatus(ctx context.Context, r *dispatcherv1.GetStatusRequest) (*dispatcherv1.GetStatusResponse, error) {
	p, e := auth(ctx, "admin")
	if e != nil {
		return nil, e
	}
	if d.lagReader == nil {
		return nil, status.Error(codes.Unavailable, "consumer lag dependency is not configured")
	}
	lag, e := d.lagReader(ctx, d.cfg.ConsumerGroupPrefix+"dispatcher", d.cfg.TopicPrefix+"task-events.v1")
	if e != nil {
		return nil, status.Error(codes.Unavailable, "consumer lag dependency unavailable")
	}
	out := &dispatcherv1.GetStatusResponse{AsOf: timestamppb.Now(), ConsumerLag: lag}
	e = d.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT CASE WHEN a.status IN ('pending','dispatched','acked','executing') THEN a.task_id END),COALESCE(SUM(a.status='dlq'),0) FROM task_dispatches a WHERE a.tenant_id=? AND a.last_task_status NOT IN (6,7,8,9,10) AND NOT EXISTS (SELECT 1 FROM task_dispatches newer WHERE newer.tenant_id=a.tenant_id AND newer.command_id=a.command_id AND newer.attempt>a.attempt)`, p.TenantID).Scan(&out.ActiveTasks, &out.DlqCount)
	if e != nil {
		return nil, unavailable(e)
	}
	return out, nil
}
func (d *Dispatcher) RetryDLQ(ctx context.Context, r *dispatcherv1.RetryDLQRequest) (*dispatcherv1.RetryDLQResponse, error) {
	p, e := auth(ctx, "admin")
	if e != nil {
		return nil, e
	}
	if e = validateID(r.TaskId, 128); e != nil {
		return nil, e
	}
	if r.Reason == "" || len(r.Reason) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "reason required (at most 1024 bytes)")
	}
	t, e := d.currentTask(ctx, p.TenantID, r.TaskId)
	if e != nil {
		return nil, e
	}
	if Terminal(t.Status) || t.Deadline == nil || !t.Deadline.AsTime().After(time.Now()) {
		return &dispatcherv1.RetryDLQResponse{Message: "task is terminal or deadline elapsed"}, nil
	}
	// 保留尝试记录的行锁，但不使用可重复读的范围锁或间隙锁：并发扫描
	// 最新尝试不能阻止获胜者追加下一次尝试。
	// dispatch/attempt 唯一键仍保证只能持久化一个重试轮次。
	tx, e := d.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return nil, unavailable(e)
	}
	defer tx.Rollback()
	a, e := scanAttempt(tx.QueryRowContext(ctx, "SELECT "+dispatchColumns+" FROM task_dispatches WHERE tenant_id=? AND task_id=? ORDER BY id DESC LIMIT 1 FOR UPDATE", p.TenantID, r.TaskId))
	if e != nil {
		return nil, unavailable(e)
	}
	if a.status != "dlq" || Terminal(a.lastStatus) {
		return &dispatcherv1.RetryDLQResponse{Message: "latest attempt is not an active DLQ"}, nil
	}
	if a.kind == "execute" && t.CancelRequested {
		return &dispatcherv1.RetryDLQResponse{Message: "execute is superseded by cancellation"}, nil
	}
	// DLQ 是历史投递凭据，晚到的 ACK 不会将其清除。进度已获确认后，
	// 无论重新查询 Task，还是读取加锁后的更新镜像，都不能授权新一轮 execute。
	// 取消命令仍需单独重试。
	if a.kind == "execute" && (t.Status == commonv1.TaskStatus_TASK_STATUS_ACKED || t.Status == commonv1.TaskStatus_TASK_STATUS_EXECUTING || a.lastStatus == commonv1.TaskStatus_TASK_STATUS_ACKED || a.lastStatus == commonv1.TaskStatus_TASK_STATUS_EXECUTING) {
		return &dispatcherv1.RetryDLQResponse{Message: "execution already acknowledged; no transport retry needed"}, nil
	}
	if e = insertAttempt(ctx, tx, t, a.kind, a.number+1, a.round+1, time.Now().UTC()); e != nil {
		if duplicate(e) {
			return &dispatcherv1.RetryDLQResponse{Message: "DLQ round already retried"}, nil
		}
		return nil, unavailable(e)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET last_error=? WHERE tenant_id=? AND dispatch_id=?`, "manual retry: "+r.Reason, p.TenantID, fmt.Sprintf("%s-%d", a.command, a.number+1)); e != nil {
		return nil, unavailable(e)
	}
	if e = tx.Commit(); e != nil {
		return nil, unavailable(e)
	}
	return &dispatcherv1.RetryDLQResponse{Accepted: true, Message: "new transport retry round persisted"}, nil
}
