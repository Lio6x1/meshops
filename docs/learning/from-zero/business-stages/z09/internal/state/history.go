package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	mysql "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func sortedBindingKeys(r *platform.Registry) []string {
	out := make([]string, 0, len(r.Bindings))
	for k := range r.Bindings {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func meters(a, b *commonv1.Location) float64 {
	rad := math.Pi / 180
	dlat := (b.Latitude - a.Latitude) * rad
	dlon := (b.Longitude - a.Longitude) * rad
	h := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 6371000 * 2 * math.Asin(math.Sqrt(math.Min(1, h)))
}
func sampleReason(previous, current *commonv1.EntityStateEvent, period time.Duration) string {
	if current.Snapshot == nil {
		return ""
	}
	if previous == nil || previous.Snapshot == nil {
		return "periodic"
	}
	a, b := previous.Snapshot, current.Snapshot
	if a.Status != b.Status {
		return "state_change"
	}
	if (a.Location == nil) != (b.Location == nil) || a.Location != nil && b.Location != nil && meters(a.Location, b.Location) >= 50 {
		return "position"
	}
	if a.Velocity != nil && b.Velocity != nil {
		old, next := a.Velocity, b.Velocity
		if old.Speed == 0 && next.Speed > 0 || old.Speed > 0 && math.Abs(next.Speed-old.Speed)/old.Speed >= 0.2 {
			return "velocity"
		}
		if old.Heading != nil && next.Heading != nil {
			d := math.Abs(*next.Heading - *old.Heading)
			d = math.Min(d, 360-d)
			if d >= 30 {
				return "velocity"
			}
		}
	}
	if current.OccurredAt.AsTime().Sub(previous.OccurredAt.AsTime()) >= period {
		return "periodic"
	}
	return ""
}
func (e *Entity) lastSample(ctx context.Context, tenant, id string) (*commonv1.EntityStateEvent, error) {
	var event commonv1.EntityStateEvent
	var occurred time.Time
	var payload []byte
	err := e.db.QueryRowContext(ctx, `SELECT event_id,source_id,source_generation,entity_version,occurred_at,snapshot FROM entity_history_samples WHERE tenant_id=? AND entity_id=? ORDER BY occurred_at DESC,id DESC LIMIT 1`, tenant, id).Scan(&event.EventId, &event.SourceId, &event.SourceGeneration, &event.EntityVersion, &occurred, &payload)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	event.Snapshot = &commonv1.EntitySnapshot{}
	if err = protojson.Unmarshal(payload, event.Snapshot); err != nil {
		return nil, err
	}
	event.TenantId = tenant
	event.EntityId = id
	event.OccurredAt = timestamppb.New(occurred.UTC())
	return &event, nil
}
func (e *Entity) Sample(ctx context.Context, raw []byte) error {
	event, err := e.decode(raw)
	if err != nil {
		quarantine(ctx, "invalid_history_event")
		return nil
	}
	key := platform.Key(event.TenantId, event.EntityId)
	if !e.historyAllowed[key] || event.Snapshot == nil {
		return nil
	}
	now := time.Now().UTC()
	if event.OccurredAt.AsTime().Before(now.Add(-7 * 24 * time.Hour)) {
		historyCandidates.WithLabelValues("age").Inc()
		slog.InfoContext(ctx, "history candidate skipped", "reason", "age")
		return nil
	}
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	previous, loaded := e.historyLast[key]
	if !loaded {
		previous, err = e.lastSample(ctx, event.TenantId, event.EntityId)
		if err != nil {
			return err
		}
		e.historyLast[key] = previous
	}
	if previous != nil && event.SourceGeneration == previous.SourceGeneration && event.EntityVersion <= previous.EntityVersion {
		return nil
	}
	reason := sampleReason(previous, event, platform.Duration(e.cfg.HistoryPeriod))
	if reason == "" {
		return nil
	}
	second := now.Unix()
	if e.budgetSecond != second {
		e.budgetSecond = second
		e.budgetTotal = 0
		e.budgetPeriodic = 0
	}
	if e.budgetTotal >= e.cfg.HistoryBudget || reason == "periodic" && e.budgetPeriodic >= 100 {
		historyCandidates.WithLabelValues("budget").Inc()
		slog.InfoContext(ctx, "history candidate skipped", "reason", "budget")
		return nil
	}
	payload, err := protojson.Marshal(event.Snapshot)
	if err != nil {
		return err
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 插入前先预留键。重复记录不能产生孤立
	// 样本，事务失败也不能留下持久化的预留记录。
	_, err = tx.ExecContext(ctx, `INSERT INTO history_sample_keys(tenant_id,source_id,source_generation,event_id,sampled_at,sample_id) VALUES(?,?,?,?,?,0)`, event.TenantId, event.SourceId, event.SourceGeneration, event.EventId, now)
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return nil
		}
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO entity_history_samples(tenant_id,entity_id,occurred_at,sampled_at,event_id,source_id,source_generation,entity_version,snapshot,sample_reason) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.TenantId, event.EntityId, event.OccurredAt.AsTime(), now, event.EventId, event.SourceId, event.SourceGeneration, event.EntityVersion, string(payload), reason)
	if err != nil {
		return err
	}
	sampleID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE history_sample_keys SET sample_id=? WHERE tenant_id=? AND source_id=? AND source_generation=? AND event_id=?`, sampleID, event.TenantId, event.SourceId, event.SourceGeneration, event.EventId)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	e.budgetTotal++
	if reason == "periodic" {
		e.budgetPeriodic++
	}
	e.historyLast[key] = event
	historyCandidates.WithLabelValues("stored").Inc()
	return nil
}
func (e *Entity) ListHistorySamples(ctx context.Context, r *entityv1.ListHistorySamplesRequest) (*entityv1.ListHistorySamplesResponse, error) {
	if err := queryRole(ctx); err != nil {
		return nil, err
	}
	if platform.Identity(ctx).Role == "task_service" {
		return nil, status.Error(codes.PermissionDenied, "history role required")
	}
	if r == nil || !validID(r.EntityId, 128) || r.StartTime == nil || r.EndTime == nil || r.StartTime.CheckValid() != nil || r.EndTime.CheckValid() != nil {
		return nil, status.Error(codes.InvalidArgument, "entity and valid time range required")
	}
	start, end := r.StartTime.AsTime(), r.EndTime.AsTime()
	if !end.After(start) || end.Sub(start) > 24*time.Hour {
		return nil, status.Error(codes.InvalidArgument, "history range must be positive and at most 24h")
	}
	size := int(r.PageSize)
	if size == 0 {
		size = 100
	}
	if size < 1 || size > 500 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be 1..500")
	}
	tenant := platform.Identity(ctx).TenantID
	sum := sha256.Sum256([]byte(r.EntityId + "|" + start.Format(time.RFC3339Nano) + "|" + end.Format(time.RFC3339Nano)))
	filter := hex.EncodeToString(sum[:])
	upper := time.Now().UTC()
	expiry := upper.Add(15 * time.Minute)
	lastTime := start
	lastID := int64(0)
	if r.PageToken != "" {
		c, err := platform.ReadCursor(r.PageToken, e.cursorKey, "history", tenant, filter, time.Now())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid history cursor")
		}
		lastID, err = strconv.ParseInt(c.LastID, 10, 64)
		if err != nil || lastID < 1 || c.LastTime.Before(start) || !c.LastTime.Before(end) || c.UpperTime.IsZero() || c.UpperTime.After(time.Now().Add(time.Second)) {
			return nil, status.Error(codes.InvalidArgument, "invalid cursor position")
		}
		lastTime = c.LastTime
		upper = c.UpperTime
		expiry = c.Expires
	}
	rows, err := e.db.QueryContext(ctx, `SELECT id,event_id,occurred_at,sampled_at,snapshot,sample_reason FROM entity_history_samples WHERE tenant_id=? AND entity_id=? AND occurred_at>=? AND occurred_at<? AND sampled_at<=? AND (occurred_at>? OR (occurred_at=? AND id>?)) ORDER BY occurred_at ASC,id ASC LIMIT ?`, tenant, r.EntityId, start, end, upper, lastTime, lastTime, lastID, size+1)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "history query failed")
	}
	defer rows.Close()
	out := &entityv1.ListHistorySamplesResponse{}
	var next bool
	for rows.Next() {
		var id int64
		var eventID, reason string
		var occurred, sampled time.Time
		var payload []byte
		if err = rows.Scan(&id, &eventID, &occurred, &sampled, &payload, &reason); err != nil {
			return nil, status.Error(codes.Unavailable, "history scan failed")
		}
		if len(out.Samples) == size {
			next = true
			break
		}
		snap := &commonv1.EntitySnapshot{}
		if err = protojson.Unmarshal(payload, snap); err != nil {
			return nil, status.Error(codes.Unavailable, "corrupt history payload")
		}
		out.Samples = append(out.Samples, &entityv1.HistorySample{SampleId: strconv.FormatInt(id, 10), EventId: eventID, OccurredAt: timestamppb.New(occurred.UTC()), SampledAt: timestamppb.New(sampled.UTC()), Snapshot: snap, SampleReason: reason})
	}
	if err = rows.Err(); err != nil {
		return nil, status.Error(codes.Unavailable, "history iteration failed")
	}
	if next {
		last := out.Samples[len(out.Samples)-1]
		out.NextPageToken, err = platform.SignCursor(platform.Cursor{Kind: "history", Tenant: tenant, Filter: filter, LastTime: last.OccurredAt.AsTime(), LastID: last.SampleId, UpperTime: upper, Expires: expiry}, e.cursorKey)
		if err != nil {
			return nil, status.Error(codes.Internal, "cursor encoding failed")
		}
	}
	return out, nil
}
func (e *Entity) pruneHistory(ctx context.Context) error {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			err := e.pruneHistoryBatch(ctx, time.Now().UTC().Add(-7*24*time.Hour))
			delay := time.Minute
			if errors.Is(err, errRetentionBacklog) {
				delay = 100 * time.Millisecond
			} else if err != nil {
				slog.WarnContext(ctx, "history retention retry", "reason", "database_unavailable")
			}
			timer.Reset(delay)
		}
	}
}

var errRetentionBacklog = errors.New("history retention catch-up remains")

func (e *Entity) pruneHistoryBatch(ctx context.Context, cutoff time.Time) error {
	budget, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for batch := 0; batch < 100; batch++ {
		n, err := e.pruneHistoryPage(budget, cutoff)
		if err != nil {
			if ctx.Err() == nil && budget.Err() != nil {
				return errRetentionBacklog
			}
			return err
		}
		if n < 500 {
			return nil
		}
	}
	return errRetentionBacklog
}
func (e *Entity) pruneHistoryPage(ctx context.Context, cutoff time.Time) (int, error) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	total := 0
	// 拆分年龄范围以保留 OR 语义，同时让每个 ORDER BY 使用
	// 现有的年龄索引。LIMIT 现在限制各范围的扫描量，避免合并并排序
	// 所有过期行。先删除第一个范围，再读取第二个范围，确保
	// 按两种时钟都已过期的事件不会重复消耗分页预算。
	for _, query := range []string{
		`SELECT id FROM entity_history_samples WHERE sampled_at<? ORDER BY sampled_at,id LIMIT ? FOR UPDATE`,
		`SELECT id FROM entity_history_samples WHERE occurred_at<? ORDER BY occurred_at,id LIMIT ? FOR UPDATE`,
	} {
		rows, err := tx.QueryContext(ctx, query, cutoff, 500-total)
		if err != nil {
			return 0, err
		}
		var ids []any
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if len(ids) > 0 {
			marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
			if _, err = tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM history_sample_keys WHERE sample_id IN (%s)", marks), ids...); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM entity_history_samples WHERE id IN (%s)", marks), ids...); err != nil {
				return 0, err
			}
			total += len(ids)
		}
		if total == 500 {
			break
		}
	}
	return total, tx.Commit()
}
