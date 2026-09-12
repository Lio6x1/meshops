package tasks

import (
	"context"
	"database/sql"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/platform"
	mysql "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

const columns = `task_id,tenant_id,idempotency_key,task_type,target_entity_id,payload,priority,status,status_version,created_at,updated_at,deadline,created_by,COALESCE(cancelled_reason,''),COALESCE(failure_reason,''),COALESCE(executor_id,''),COALESCE(execution_key,''),cancel_requested,COALESCE(result,''),completed_at,COALESCE(request_hash,'')`

type scanner interface{ Scan(...any) error }

func readTask(row scanner) (*commonv1.Task, string, error) {
	t := &commonv1.Task{Payload: &commonv1.TaskPayload{}}
	var st, hash string
	var created, updated time.Time
	var deadline, completed sql.NullTime
	e := row.Scan(&t.TaskId, &t.TenantId, &t.IdempotencyKey, &t.TaskType, &t.TargetEntityId, &t.Payload.PayloadJson, &t.Priority, &st, &t.StatusVersion, &created, &updated, &deadline, &t.CreatedBy, &t.CancelledReason, &t.FailureReason, &t.ExecutorId, &t.ExecutionKey, &t.CancelRequested, &t.ResultJson, &completed, &hash)
	if e != nil {
		return nil, "", e
	}
	t.Status = parseStatus(st)
	t.CreatedAt = timestamppb.New(created)
	t.UpdatedAt = timestamppb.New(updated)
	if deadline.Valid {
		t.Deadline = timestamppb.New(deadline.Time)
	}
	if completed.Valid {
		t.CompletedAt = timestamppb.New(completed.Time)
	}
	return t, hash, nil
}
func unavailable(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return status.Error(codes.NotFound, "object not found")
	}
	if e == nil {
		return nil
	}
	return status.Error(codes.Unavailable, "persistent dependency unavailable")
}
func duplicate(e error) bool { var m *mysql.MySQLError; return errors.As(e, &m) && m.Number == 1062 }
func writeAudit(ctx context.Context, tx *sql.Tx, t *commonv1.Task, from commonv1.TaskStatus, actor, reason, id string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO task_status_history(tenant_id,task_id,from_status,to_status,status_version,changed_at,changed_by,reason,event_id) VALUES(?,?,?,?,?,?,?,?,?)`, t.TenantId, t.TaskId, statusName(from), statusName(t.Status), t.StatusVersion, t.UpdatedAt.AsTime(), actor, reason, id)
	return e
}
func writeEvent(ctx context.Context, tx *sql.Tx, t *commonv1.Task, kind commonv1.TaskEventType) error {
	data, e := protojson.Marshal(t)
	if e != nil {
		return e
	}
	event := &commonv1.TaskEvent{EventId: platform.NewID(), TaskId: t.TaskId, TenantId: t.TenantId, EventType: kind, CurrentStatus: t.Status, StatusVersion: t.StatusVersion, OccurredAt: t.UpdatedAt, DataJson: string(data)}
	b, e := protojson.Marshal(event)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO outbox_events(event_type,aggregate_type,aggregate_id,payload,event_id,tenant_id) VALUES(?,'task',?,?,?,?)`, kind.String(), t.TaskId, b, event.EventId, t.TenantId)
	return e
}
func persistChange(ctx context.Context, tx *sql.Tx, t *commonv1.Task, from commonv1.TaskStatus, actor, reason, id string, kind commonv1.TaskEventType) error {
	var completed any
	if t.CompletedAt != nil {
		completed = t.CompletedAt.AsTime()
	}
	var result any
	if t.ResultJson != "" {
		result = t.ResultJson
	}
	_, e := tx.ExecContext(ctx, `UPDATE tasks SET status=?,status_version=?,updated_at=?,cancel_requested=?,cancelled_reason=?,failure_reason=?,result=?,completed_at=? WHERE tenant_id=? AND task_id=?`, statusName(t.Status), t.StatusVersion, t.UpdatedAt.AsTime(), t.CancelRequested, t.CancelledReason, t.FailureReason, result, completed, t.TenantId, t.TaskId)
	if e != nil {
		return e
	}
	if e = writeAudit(ctx, tx, t, from, actor, reason, id); e != nil {
		return e
	}
	return writeEvent(ctx, tx, t, kind)
}
