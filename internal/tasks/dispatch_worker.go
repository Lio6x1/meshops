package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"time"
)

func (d *Dispatcher) DispatchDue(ctx context.Context) error {
	for i := 0; i < 100; i++ {
		found, e := d.dispatchOne(ctx)
		if e != nil {
			return e
		}
		if !found {
			return nil
		}
	}
	return nil
}
func (d *Dispatcher) dispatchOne(ctx context.Context) (bool, error) {
	tx, e := d.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	a, e := scanAttempt(tx.QueryRowContext(ctx, "SELECT "+dispatchColumns+` FROM task_dispatches a WHERE status IN ('pending','dispatched') AND next_attempt_at<=UTC_TIMESTAMP(6) AND (lease_until IS NULL OR lease_until<UTC_TIMESTAMP(6)) AND last_task_status NOT IN (6,7,8,9,10) AND NOT EXISTS(SELECT 1 FROM task_dispatches newer WHERE newer.tenant_id=a.tenant_id AND newer.command_id=a.command_id AND newer.attempt>a.attempt) ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`))
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	owner := platform.NewID()
	if _, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET lease_owner=?,lease_until=? WHERE id=?`, owner, time.Now().UTC().Add(30*time.Second), a.id); e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	release := func(delay time.Duration) error {
		_, e := d.db.ExecContext(ctx, `UPDATE task_dispatches SET lease_owner=NULL,lease_until=NULL,next_attempt_at=? WHERE id=? AND lease_owner=?`, time.Now().UTC().Add(delay), a.id, owner)
		return e
	}
	t, e := d.currentTask(ctx, a.tenant, a.task)
	if e != nil {
		release(time.Second)
		return false, e
	}
	if t.ExecutorId != a.executor || t.ExecutionKey != a.key {
		release(30 * time.Second)
		return false, errors.New("persisted dispatch binding mismatch")
	}
	if !Terminal(t.Status) && (t.Deadline == nil || !t.Deadline.AsTime().After(time.Now())) {
		return true, release(time.Second)
	}
	tx, e = d.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var liveOwner sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT lease_owner FROM task_dispatches WHERE id=? FOR UPDATE`, a.id).Scan(&liveOwner); e != nil {
		return false, e
	}
	if liveOwner.String != owner {
		return true, nil
	}
	// 租约只防止其他发送 worker 领取，不能阻止 Kafka 更新同一投递记录。
	// 必须重读投递状态/镜像版本，避免用 RPC 前的快照覆盖已提交的 ACK 或终态。
	a, e = scanAttempt(tx.QueryRowContext(ctx, "SELECT "+dispatchColumns+" FROM task_dispatches WHERE id=?", a.id))
	if e != nil {
		return false, e
	}
	if a.version > t.StatusVersion || a.status != "pending" && a.status != "dispatched" {
		if _, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET lease_owner=NULL,lease_until=NULL,next_attempt_at=? WHERE id=? AND lease_owner=?`, time.Now().UTC().Add(time.Second), a.id, owner); e != nil {
			return false, e
		}
		return true, tx.Commit()
	}
	if e = mirrorTask(ctx, tx, t); e != nil {
		return false, e
	}
	if Terminal(t.Status) || a.kind == "execute" && (t.CancelRequested || t.Status == commonv1.TaskStatus_TASK_STATUS_ACKED || t.Status == commonv1.TaskStatus_TASK_STATUS_EXECUTING) {
		if t.CancelRequested && !Terminal(t.Status) {
			if _, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET status='abandoned' WHERE id=? AND status IN ('pending','dispatched')`, a.id); e != nil {
				return false, e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET lease_owner=NULL,lease_until=NULL WHERE id=? AND lease_owner=?`, a.id, owner); e != nil {
			return false, e
		}
		return true, tx.Commit()
	}
	if a.status == "dispatched" {
		var attempts int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dispatches WHERE tenant_id=? AND command_id=? AND retry_round=?`, a.tenant, a.command, a.round).Scan(&attempts); e != nil {
			return false, e
		}
		if attempts >= d.cfg.MaxAttempts {
			_, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET status='dlq',dlq_at=UTC_TIMESTAMP(6),last_error='ACK timeout; automatic round exhausted',lease_owner=NULL,lease_until=NULL WHERE id=?`, a.id)
		} else {
			_, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET status='timeout',completed_at=UTC_TIMESTAMP(6),last_error='ACK timeout',lease_owner=NULL,lease_until=NULL WHERE id=?`, a.id)
			if e == nil {
				delay := platform.Duration(d.cfg.RetryDelays[attempts-1])
				e = insertAttempt(ctx, tx, t, a.kind, a.number+1, a.round, time.Now().UTC().Add(delay))
			}
		}
		if e != nil {
			return false, e
		}
		return true, tx.Commit()
	}
	// Durable transport intent precedes stream send; interruption consumes this
	// attempt after its ACK deadline and creates the next immutable attempt.
	command := &executorv1.ListenTasksResponse{}
	if len(a.payload) == 0 || protojson.Unmarshal(a.payload, command) != nil {
		return false, errors.New("legacy dispatch has no valid command_payload; explicit migration review required")
	}
	if _, e = tx.ExecContext(ctx, `UPDATE task_dispatches SET status='dispatched',dispatched_at=UTC_TIMESTAMP(6),next_attempt_at=?,lease_owner=NULL,lease_until=NULL WHERE id=?`, time.Now().UTC().Add(platform.Duration(d.cfg.AckTimeout)), a.id); e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	if e = d.send(ctx, a.tenant, a.executor, command); e != nil {
		_, writeErr := d.db.ExecContext(ctx, `UPDATE task_dispatches SET last_error='stream unavailable or send failed; awaiting ACK deadline' WHERE id=?`, a.id)
		return true, writeErr
	}
	if a.kind == "execute" {
		if e = d.reportDispatched(ctx, t, a.idString); e != nil {
			slog.Warn("DISPATCHED report deferred; executor ACK remains legal", "task_id", t.TaskId, "code", status.Code(e).String())
		}
	}
	return true, nil
}
func (d *Dispatcher) reportDispatched(ctx context.Context, t *commonv1.Task, dispatchID string) error {
	for i := 0; i < 3; i++ {
		if t.Status != commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING || t.CancelRequested {
			return nil
		}
		rpc, cancel, e := internalContext(ctx, d.reg, t.TenantId, "dispatcher_service", platform.Duration(d.cfg.UnaryTimeout))
		if e != nil {
			return e
		}
		_, e = d.task.ReportTaskStatus(rpc, &taskv1.ReportTaskStatusRequest{EventId: platform.NewID(), TaskId: t.TaskId, ExecutionKey: t.ExecutionKey, ExecutorId: t.ExecutorId, DispatchId: dispatchID, ExpectedStatusVersion: t.StatusVersion, Status: commonv1.TaskStatus_TASK_STATUS_DISPATCHED, OccurredAt: timestamppb.Now()})
		cancel()
		if e == nil {
			return nil
		}
		if status.Code(e) != codes.Aborted && status.Code(e) != codes.FailedPrecondition {
			return e
		}
		t, e = d.currentTask(ctx, t.TenantId, t.TaskId)
		if e != nil {
			return e
		}
	}
	return nil
}
func (d *Dispatcher) PublishDLQ(ctx context.Context, b Bus) error {
	rows, e := d.db.QueryContext(ctx, `SELECT tenant_id,task_id,dispatch_id,command_id,execution_key,attempt,retry_round FROM task_dispatches WHERE dlq_at IS NOT NULL AND dlq_published_at IS NULL ORDER BY id LIMIT 100`)
	if e != nil {
		return e
	}
	type notification struct {
		TenantID     string `json:"tenantId"`
		TaskID       string `json:"taskId"`
		DispatchID   string `json:"dispatchId"`
		CommandID    string `json:"commandId"`
		ExecutionKey string `json:"executionKey"`
		Attempt      int    `json:"attempt"`
		RetryRound   int    `json:"retryRound"`
	}
	var pending []notification
	for rows.Next() {
		var n notification
		if e = rows.Scan(&n.TenantID, &n.TaskID, &n.DispatchID, &n.CommandID, &n.ExecutionKey, &n.Attempt, &n.RetryRound); e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, n := range pending {
		raw, _ := json.Marshal(n)
		send, cancel := context.WithTimeout(ctx, 5*time.Second)
		e = b.Publish(send, d.cfg.TopicPrefix+"task-dlq.v1", n.TenantID+":"+n.TaskID, raw)
		cancel()
		if e != nil {
			return fmt.Errorf("DLQ notification publish failed: %w", e)
		}
		if _, e = d.db.ExecContext(ctx, `UPDATE task_dispatches SET dlq_published_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND dispatch_id=? AND dlq_published_at IS NULL`, n.TenantID, n.DispatchID); e != nil {
			return e
		}
	}
	return nil
}
