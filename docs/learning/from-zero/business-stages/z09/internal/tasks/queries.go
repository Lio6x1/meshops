package tasks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

type cursor struct {
	V        int       `json:"v"`
	Kind     string    `json:"kind"`
	Tenant   string    `json:"tenant_id"`
	Filter   string    `json:"filter_hash"`
	LastTime time.Time `json:"last_time"`
	LastID   string    `json:"last_id"`
	Upper    time.Time `json:"upper_time"`
	Expires  time.Time `json:"expires_at"`
}

func seal(c cursor, key []byte) string {
	b, _ := json.Marshal(c)
	h := hmac.New(sha256.New, key)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(append(b, h.Sum(nil)...))
}
func unseal(token string, key []byte, tenant, filter string) (cursor, error) {
	var c cursor
	bad := fmt.Errorf("invalid or expired page token")
	if len(token) > 2048 {
		return c, bad
	}
	b, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil || len(b) < 33 {
		return c, bad
	}
	payload, signature := b[:len(b)-32], b[len(b)-32:]
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	if !hmac.Equal(signature, h.Sum(nil)) || json.Unmarshal(payload, &c) != nil || c.V != 1 || c.Kind != "tasks" || c.Tenant != tenant || c.Filter != filter || !c.Expires.After(time.Now()) || c.LastTime.IsZero() || c.Upper.IsZero() || c.LastTime.After(c.Upper) || validateID(c.LastID, 128) != nil {
		return c, bad
	}
	return c, nil
}
func (s *Service) ListTasks(ctx context.Context, r *taskv1.ListTasksRequest) (*taskv1.ListTasksResponse, error) {
	p, e := auth(ctx, "operator", "admin")
	if e != nil {
		return nil, e
	}
	if r.TargetEntityId != "" {
		if e = validateID(r.TargetEntityId, 128); e != nil {
			return nil, e
		}
	}
	n := r.PageSize
	if n == 0 {
		n = 20
	}
	if n < 1 || n > 100 {
		return nil, status.Error(codes.InvalidArgument, "page_size must be 1..100")
	}
	if _, ok := commonv1.TaskStatus_name[int32(r.Status)]; !ok {
		return nil, status.Error(codes.InvalidArgument, "invalid task status")
	}
	filterBytes, _ := json.Marshal([]any{r.TargetEntityId, r.Status})
	filter := digest(filterBytes)
	c := cursor{V: 1, Kind: "tasks", Tenant: p.TenantID, Filter: filter, Upper: time.Now().UTC().Truncate(time.Microsecond), Expires: time.Now().UTC().Add(15 * time.Minute)}
	if r.PageToken != "" {
		c, e = unseal(r.PageToken, s.cursorKey, p.TenantID, filter)
		if e != nil {
			return nil, status.Error(codes.InvalidArgument, e.Error())
		}
	}
	where := "tenant_id=? AND created_at<=?"
	args := []any{p.TenantID, c.Upper}
	if r.TargetEntityId != "" {
		where += " AND target_entity_id=?"
		args = append(args, r.TargetEntityId)
	}
	if r.Status != 0 {
		where += " AND status=?"
		args = append(args, statusName(r.Status))
	}
	response := &taskv1.ListTasksResponse{}
	if e = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE "+where, args...).Scan(&response.TotalCount); e != nil {
		return nil, unavailable(e)
	}
	if c.LastID != "" {
		where += " AND (created_at<? OR (created_at=? AND task_id<?))"
		args = append(args, c.LastTime, c.LastTime, c.LastID)
	}
	args = append(args, n+1)
	rows, e := s.db.QueryContext(ctx, "SELECT "+columns+" FROM tasks WHERE "+where+" ORDER BY created_at DESC,task_id DESC LIMIT ?", args...)
	if e != nil {
		return nil, unavailable(e)
	}
	defer rows.Close()
	for rows.Next() {
		t, _, e := readTask(rows)
		if e != nil {
			return nil, unavailable(e)
		}
		response.Tasks = append(response.Tasks, t)
	}
	if e = rows.Err(); e != nil {
		return nil, unavailable(e)
	}
	if len(response.Tasks) > int(n) {
		response.Tasks = response.Tasks[:n]
		last := response.Tasks[n-1]
		c.LastID = last.TaskId
		c.LastTime = last.CreatedAt.AsTime()
		response.NextPageToken = seal(c, s.cursorKey)
	}
	return response, nil
}
func (s *Service) GetTaskHistory(ctx context.Context, r *taskv1.GetTaskHistoryRequest) (*taskv1.GetTaskHistoryResponse, error) {
	p, e := auth(ctx, "operator", "admin")
	if e != nil {
		return nil, e
	}
	if e = validateID(r.TaskId, 128); e != nil {
		return nil, e
	}
	if _, _, e = s.get(ctx, p.TenantID, r.TaskId); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT from_status,to_status,changed_at,changed_by,COALESCE(reason,''),status_version,COALESCE(event_id,'') FROM task_status_history WHERE tenant_id=? AND task_id=? ORDER BY status_version LIMIT 1001`, p.TenantID, r.TaskId)
	if e != nil {
		return nil, unavailable(e)
	}
	defer rows.Close()
	out := &taskv1.GetTaskHistoryResponse{}
	for rows.Next() {
		h := &commonv1.TaskStatusChange{}
		var from, to string
		var at time.Time
		if e = rows.Scan(&from, &to, &at, &h.ChangedBy, &h.Reason, &h.StatusVersion, &h.EventId); e != nil {
			return nil, unavailable(e)
		}
		h.FromStatus = parseStatus(from)
		h.ToStatus = parseStatus(to)
		h.ChangedAt = timestamppb.New(at)
		out.History = append(out.History, h)
	}
	if e = rows.Err(); e != nil {
		return nil, unavailable(e)
	}
	if len(out.History) > 1000 {
		return nil, status.Error(codes.ResourceExhausted, "task audit exceeds 1000 entries")
	}
	return out, nil
}
