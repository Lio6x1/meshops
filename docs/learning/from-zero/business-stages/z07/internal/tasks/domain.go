// Package tasks implements the durable task fact and transport attempt stores.
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
		return p, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("one JSON object required")
	}
	if p.DurationSeconds < 1 || p.DurationSeconds > 60 || len(p.Note) > 256 {
		return p, fmt.Errorf("duration_seconds must be 1..60; note at most 256 UTF-8 bytes")
	}
	// Reject duplicate fields and null scalar values rather than silently last-wins.
	d = json.NewDecoder(strings.NewReader(raw))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return p, fmt.Errorf("object required")
	}
	seen := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		if e != nil {
			return p, e
		}
		k := t.(string)
		if seen[k] {
			return p, fmt.Errorf("duplicate parameter %s", k)
		}
		seen[k] = true
		var v json.RawMessage
		if e = d.Decode(&v); e != nil {
			return p, e
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
