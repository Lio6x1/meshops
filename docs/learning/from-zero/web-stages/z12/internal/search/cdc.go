// Package search 提供仅面向任务的最终一致搜索投影。
package search

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/tasks"
)

// Document 明确定义可搜索字段白名单。MySQL 模式扩展时，
// 不能意外将原始载荷、结果或操作方凭据纳入搜索。
type Document struct {
	TenantID        string `json:"tenant_id"`
	TaskID          string `json:"task_id"`
	TargetEntityID  string `json:"target_entity_id"`
	TaskType        string `json:"task_type"`
	Status          string `json:"status"`
	StatusVersion   int64  `json:"status_version"`
	Note            string `json:"note"`
	CancelledReason string `json:"cancelled_reason"`
	FailureReason   string `json:"failure_reason"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// JSON 无歧义地编码元组；无填充的 URL 安全 Base64 编码可安全用于 ES 路径。
func (d Document) ID() string {
	b, _ := json.Marshal([2]string{d.TenantID, d.TaskID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func (d Document) Version() int64 { return d.StatusVersion + 1 }

// DecodeCanal 接收 MySQL binlog_row_image=FULL 配置下的 Canal 扁平 JSON。
// 任一行格式错误都会使整条消息失败，防止调用者只应用部分数据后
// 就提交 Kafka 位点。错误信息刻意省略字段值。
func DecodeCanal(data []byte, database string) ([]Document, error) {
	if len(data) == 0 || len(data) > 4<<20 || !utf8.Valid(data) || database == "" {
		return nil, fmt.Errorf("invalid CDC message size, encoding or database")
	}
	var event struct {
		Database  string                       `json:"database"`
		Table     string                       `json:"table"`
		Type      string                       `json:"type"`
		DDL       bool                         `json:"isDdl"`
		Data      []map[string]json.RawMessage `json:"data"`
		MySQLType map[string]string            `json:"mysqlType"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("invalid Canal JSON")
	}
	// 即使表过滤器只包含任务表，Canal 仍会转发数据库级 QUERY/DDL 事件。
	// 其他数据库的 DDL 不会影响本投影。其他数据库的数据行，
	// 以及所有影响任务数据库的 DDL，仍一律拒绝处理。
	if event.DDL && event.Database != "" && event.Database != database && len(event.Data) == 0 {
		return []Document{}, nil
	}
	if event.Database != database || event.Table != "tasks" {
		return nil, fmt.Errorf("unexpected CDC database or table")
	}
	if event.DDL || (event.Type != "INSERT" && event.Type != "UPDATE") {
		return nil, fmt.Errorf("unsupported task CDC operation; recovery required")
	}
	if len(event.Data) == 0 {
		return nil, fmt.Errorf("empty task CDC rows")
	}
	docs := make([]Document, 0, len(event.Data))
	for i, row := range event.Data {
		// MySQL 在行事件中将 ENUM 编码为从 1 开始的序号。Canal 1.1.8
		// 同时提供该序号与列的枚举声明。
		var rawStatus string
		if json.Unmarshal(row["status"], &rawStatus) == nil {
			if ordinal, e := strconv.Atoi(rawStatus); e == nil {
				labels := []string{"CREATED", "DISPATCH_PENDING", "DISPATCHED", "ACKED", "EXECUTING", "SUCCEEDED", "FAILED", "CANCELLED", "TIMED_OUT", "REJECTED"}
				expected := "enum('" + strings.Join(labels, "','") + "')"
				if ordinal < 1 || ordinal > len(labels) || strings.ReplaceAll(event.MySQLType["status"], " ", "") != expected {
					return nil, fmt.Errorf("CDC status ENUM schema mismatch")
				}
				row["status"], _ = json.Marshal(labels[ordinal-1])
			}
		}
		d, err := decodeRow(row)
		if err != nil {
			return nil, fmt.Errorf("CDC row %d: %w", i, err)
		}
		docs = append(docs, d)
	}
	return docs, nil
}

func decodeRow(row map[string]json.RawMessage) (Document, error) {
	var d Document
	var payload, version string
	for _, field := range []struct {
		name     string
		dst      *string
		nullable bool
		max      int
	}{
		{"tenant_id", &d.TenantID, false, 64}, {"task_id", &d.TaskID, false, 128},
		{"target_entity_id", &d.TargetEntityID, false, 128}, {"task_type", &d.TaskType, false, 64},
		{"status", &d.Status, false, 32}, {"status_version", &version, false, 10},
		{"payload", &payload, false, 4096}, {"created_at", &d.CreatedAt, false, 32},
		{"updated_at", &d.UpdatedAt, false, 32},
		{"cancelled_reason", &d.CancelledReason, true, 65535}, {"failure_reason", &d.FailureReason, true, 65535},
	} {
		raw, ok := row[field.name]
		if !ok {
			return d, fmt.Errorf("missing FULL image column %s", field.name)
		}
		if string(raw) == "null" && field.nullable {
			continue
		}
		if string(raw) == "null" || json.Unmarshal(raw, field.dst) != nil || len(*field.dst) > field.max || (!field.nullable && strings.TrimSpace(*field.dst) == "") {
			return d, fmt.Errorf("invalid column %s", field.name)
		}
	}
	v, err := strconv.ParseInt(version, 10, 32)
	if err != nil || v < 0 {
		return d, fmt.Errorf("invalid status_version")
	}
	d.StatusVersion = v
	if _, ok := commonv1.TaskStatus_value["TASK_STATUS_"+d.Status]; !ok || d.Status == "UNSPECIFIED" {
		return d, fmt.Errorf("unknown task status")
	}
	if d.TaskType != "inspect" {
		return d, fmt.Errorf("unsupported task type")
	}
	p, err := tasks.ParseInspect(payload)
	if err != nil {
		return d, fmt.Errorf("invalid inspect payload")
	}
	d.Note = p.Note
	// MySQL 连接、会话及 Canal 均配置为 UTC。TIMESTAMP 的扁平值
	// 不包含时区偏移，因此绝不能使用机器本地时区。
	for _, value := range []*string{&d.CreatedAt, &d.UpdatedAt} {
		t, err := time.ParseInLocation("2006-01-02 15:04:05.999999", *value, time.UTC)
		if err != nil {
			return d, fmt.Errorf("invalid task timestamp")
		}
		*value = t.Format(time.RFC3339Nano)
	}
	return d, nil
}
