// Package tasks 实现任务事实与传输尝试的持久化存储。
package tasks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"strings"
	"unicode/utf8"
)

type InspectParams struct {
	DurationSeconds int64  `json:"duration_seconds"`
	Note            string `json:"note,omitempty"`
}

func ParseInspect(raw string) (InspectParams, error) {
	var p InspectParams
	if !utf8.ValidString(raw) {
		return p, fmt.Errorf("payload must be UTF-8")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		// Decoder 的 unknown-field 错误会原样回显任意长键名；公开错误保持固定长度。
		return p, fmt.Errorf("invalid inspect JSON or unsupported parameter")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("one JSON object required")
	}
	if p.DurationSeconds < 1 || p.DurationSeconds > 60 || len(p.Note) > 256 {
		return p, fmt.Errorf("duration_seconds must be 1..60; note at most 256 UTF-8 bytes")
	}
	// encoding/json 会忽略字段名大小写；协议只接受精确键名，并拒绝重复/null，
	// 防止 duration_seconds 与 DURATION_SECONDS 绕过重复检查、产生后写覆盖。
	d = json.NewDecoder(strings.NewReader(raw))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return p, fmt.Errorf("object required")
	}
	seen := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return p, fmt.Errorf("invalid inspect object")
		}
		k := t.(string)
		if k != "duration_seconds" && k != "note" {
			return p, fmt.Errorf("unsupported inspect parameter")
		}
		if seen[k] {
			return p, fmt.Errorf("duplicate parameter %s", k)
		}
		seen[k] = true
		var v json.RawMessage
		if e = d.Decode(&v); e != nil {
			return p, fmt.Errorf("invalid inspect parameter value")
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return p, fmt.Errorf("null parameter %s", k)
		}
	}
	return p, nil
}
func NormalizePriority(v int32) (int32, error) {
	if v == 0 || v == 5 {
		return 5, nil
	}
	return 0, fmt.Errorf("only priority 5 is supported")
}
func Terminal(s commonv1.TaskStatus) bool {
	return s >= commonv1.TaskStatus_TASK_STATUS_SUCCEEDED && s <= commonv1.TaskStatus_TASK_STATUS_REJECTED
}
func CanTransition(from, to commonv1.TaskStatus, cancel bool, role string) bool {
	if Terminal(from) {
		return false
	}
	if role == "dispatcher_service" {
		return to == commonv1.TaskStatus_TASK_STATUS_DISPATCHED && from == commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
	}
	if role == "system" {
		return to == commonv1.TaskStatus_TASK_STATUS_TIMED_OUT
	}
	if role != "executor" {
		return false
	}
	switch to {
	case commonv1.TaskStatus_TASK_STATUS_ACKED:
		return from == commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING || from == commonv1.TaskStatus_TASK_STATUS_DISPATCHED
	case commonv1.TaskStatus_TASK_STATUS_EXECUTING:
		return from == commonv1.TaskStatus_TASK_STATUS_ACKED
	case commonv1.TaskStatus_TASK_STATUS_SUCCEEDED, commonv1.TaskStatus_TASK_STATUS_FAILED:
		return from == commonv1.TaskStatus_TASK_STATUS_EXECUTING
	case commonv1.TaskStatus_TASK_STATUS_REJECTED:
		return from == commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING || from == commonv1.TaskStatus_TASK_STATUS_DISPATCHED
	case commonv1.TaskStatus_TASK_STATUS_CANCELLED:
		return cancel
	}
	return false
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func normalizeCreate(r *taskv1.CreateTaskRequest) (string, string, int32, error) {
	if r.TaskType != "inspect" {
		return "", "", 0, status.Error(codes.Unimplemented, "only inspect is implemented")
	}
	p, e := ParseInspect(r.GetPayload().GetPayloadJson())
	if e != nil {
		return "", "", 0, status.Error(codes.InvalidArgument, e.Error())
	}
	priority, e := NormalizePriority(r.Priority)
	if e != nil {
		return "", "", 0, status.Error(codes.InvalidArgument, e.Error())
	}
	b, _ := json.Marshal(p)
	deadline := ""
	// 幂等摘要保留“是否显式提供 deadline”，不把首次生成的默认时间混入摘要；
	// 否则同一无 deadline 请求稍后重试会被误判为参数冲突。
	if r.Deadline != nil {
		if e = r.Deadline.CheckValid(); e != nil {
			return "", "", 0, status.Error(codes.InvalidArgument, "invalid deadline")
		}
		deadline = r.Deadline.AsTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	h, _ := json.Marshal([]any{r.TaskType, r.TargetEntityId, priority, string(b), r.Deadline != nil, deadline})
	return string(b), digest(h), priority, nil
}
func statusName(s commonv1.TaskStatus) string { return strings.TrimPrefix(s.String(), "TASK_STATUS_") }
func parseStatus(s string) commonv1.TaskStatus {
	return commonv1.TaskStatus(commonv1.TaskStatus_value["TASK_STATUS_"+s])
}
func eventType(s commonv1.TaskStatus) commonv1.TaskEventType {
	return commonv1.TaskEventType(commonv1.TaskEventType_value["TASK_EVENT_TYPE_"+statusName(s)])
}
