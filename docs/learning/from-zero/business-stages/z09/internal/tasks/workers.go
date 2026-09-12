package tasks

import (
	"context"
	"database/sql"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"sync"
	"time"
)

type Bus interface {
	Publish(context.Context, string, string, []byte) error
	Consume(context.Context, string, string, func(context.Context, []byte) error) error
}

func (s *Service) Run(ctx context.Context, b Bus) error {
	// Kafka 发布阻塞不能推迟截止时间屏障的执行。
	var workers sync.WaitGroup
	start := func(name string, period time.Duration, work func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(period)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if e := work(ctx); e != nil && ctx.Err() == nil {
						slog.Error(name, "error", e)
					}
				}
			}
		}()
	}
	start("outbox retry", platform.Duration(s.cfg.OutboxPoll), func(ctx context.Context) error { return s.PublishOutbox(ctx, b) })
	start("timeout scan retry", time.Second, s.ExpireTasks)
	<-ctx.Done()
	workers.Wait()
	return ctx.Err()
}

// PublishOutbox 只领取每个任务中 ID 最小的未发布记录。租约过期
// 可能导致重复发布，但绝不能据此越过该记录发布更大的 ID。
func (s *Service) PublishOutbox(ctx context.Context, b Bus) error {
	for i := 0; i < 100; i++ {
		ok, e := s.publishOne(ctx, b)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
	}
	return nil
}
func (s *Service) publishOne(ctx context.Context, b Bus) (bool, error) {
	// 每个任务只领取最早未发布事件；租约过期可重发同一事件，但不能越过它。
	// 领取事务先提交再调用 Kafka，成功后按 owner 标记，网络不占用数据库行锁。
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var id int64
	var payload []byte
	var tenant, task string
	var retries int
	e = tx.QueryRowContext(ctx, `SELECT o.id,o.payload,COALESCE(o.tenant_id,''),o.aggregate_id,o.retry_count FROM outbox_events o WHERE o.published_at IS NULL AND o.next_attempt_at<=UTC_TIMESTAMP(6) AND (o.lease_until IS NULL OR o.lease_until<UTC_TIMESTAMP(6)) AND NOT EXISTS (SELECT 1 FROM outbox_events older WHERE older.aggregate_type=o.aggregate_type AND older.aggregate_id=o.aggregate_id AND older.published_at IS NULL AND older.id<o.id) ORDER BY o.id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &payload, &tenant, &task, &retries)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	owner := platform.NewID()
	lease := time.Now().UTC().Add(platform.Duration(s.cfg.OutboxLease))
	if _, e = tx.ExecContext(ctx, `UPDATE outbox_events SET lease_owner=?,lease_until=? WHERE id=?`, owner, lease, id); e != nil {
		return false, e
	}
	if e = tx.Commit(); e != nil {
		return false, e
	}
	event := &commonv1.TaskEvent{}
	e = protojson.Unmarshal(payload, event)
	if e == nil && (event.EventId == "" || event.TenantId == "" || event.TaskId != task) {
		e = errors.New("invalid persisted task event")
	}
	if e == nil {
		var raw []byte
		raw, e = proto.Marshal(event)
		if e == nil {
			send, cancel := context.WithTimeout(ctx, 5*time.Second)
			e = b.Publish(send, s.cfg.TopicPrefix+"task-events.v1", event.TenantId+":"+task, raw)
			cancel()
		}
	}
	if e == nil {
		_, e = s.db.ExecContext(ctx, `UPDATE outbox_events SET published_at=UTC_TIMESTAMP(6),lease_owner=NULL,lease_until=NULL,last_error=NULL WHERE id=? AND lease_owner=?`, id, owner)
		return true, e
	}
	delay := time.Second * time.Duration(1<<min(retries, 5))
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	_, updateErr := s.db.ExecContext(ctx, `UPDATE outbox_events SET retry_count=retry_count+1,last_error=?,next_attempt_at=?,lease_owner=NULL,lease_until=NULL WHERE id=? AND lease_owner=?`, "publish or event validation failed", time.Now().UTC().Add(delay), id, owner)
	if updateErr != nil {
		return false, updateErr
	}
	slog.Warn("outbox publication retained for retry", "outbox_id", id, "tenant", tenant)
	return true, nil
}
func (s *Service) ExpireTasks(ctx context.Context) error {
	rows, e := s.db.QueryContext(ctx, `SELECT tenant_id,task_id FROM tasks WHERE deadline<=UTC_TIMESTAMP(6) AND status IN ('CREATED','DISPATCH_PENDING','DISPATCHED','ACKED','EXECUTING') LIMIT 100`)
	if e != nil {
		return e
	}
	type key struct{ tenant, id string }
	var keys []key
	for rows.Next() {
		var k key
		if e = rows.Scan(&k.tenant, &k.id); e != nil {
			rows.Close()
			return e
		}
		keys = append(keys, k)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, k := range keys {
		if e = s.expireOne(ctx, k.tenant, k.id); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) expireOne(ctx context.Context, tenant, id string) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	t, _, e := readTask(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM tasks WHERE tenant_id=? AND task_id=? FOR UPDATE", tenant, id))
	if e != nil {
		return e
	}
	now := time.Now().UTC()
	if Terminal(t.Status) || t.Deadline == nil || t.Deadline.AsTime().After(now) {
		return nil
	}
	from := t.Status
	t.Status = commonv1.TaskStatus_TASK_STATUS_TIMED_OUT
	t.StatusVersion++
	t.UpdatedAt = timestamppb.New(now)
	t.CompletedAt = t.UpdatedAt
	t.FailureReason = "deadline elapsed"
	if e = persistChange(ctx, tx, t, from, "system", "deadline elapsed", platform.NewID(), commonv1.TaskEventType_TASK_EVENT_TYPE_TIMED_OUT); e != nil {
		return e
	}
	return tx.Commit()
}
