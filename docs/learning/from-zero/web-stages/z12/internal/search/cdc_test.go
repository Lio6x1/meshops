package search

import (
	"encoding/json"
	"strings"
	"testing"
)

func taskRow() map[string]any {
	return map[string]any{
		"tenant_id": "tenant-a", "task_id": "task-1", "target_entity_id": "drone-001",
		"task_type": "inspect", "status": "DISPATCH_PENDING", "status_version": "1",
		"payload":    `{"duration_seconds":1,"note":"检查屋顶"}`,
		"created_at": "2026-09-11 08:00:00.123456", "updated_at": "2026-09-11 08:00:01",
		"cancelled_reason": nil, "failure_reason": nil,
		"created_by": "do-not-index", "result": "do-not-index", "idempotency_key": "do-not-index",
	}
}

func canalMessage(rows ...map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"database": "meshops_course", "table": "tasks", "type": "UPDATE", "isDdl": false, "data": rows})
	return b
}

func TestCanalFullRowsAndSafeProjection(t *testing.T) {
	a, b := taskRow(), taskRow()
	b["task_id"], b["status_version"] = "task-2", "0"
	docs, err := DecodeCanal(canalMessage(a, b), "meshops_course")
	if err != nil || len(docs) != 2 {
		t.Fatalf("docs=%v err=%v", docs, err)
	}
	if docs[0].Note != "检查屋顶" || docs[0].CreatedAt != "2026-09-11T08:00:00.123456Z" {
		t.Fatalf("projection: %+v", docs[0])
	}
	if docs[0].Version() != 2 || docs[1].Version() != 1 {
		t.Fatal("external versions must be positive")
	}
	raw, _ := json.Marshal(docs[0])
	if strings.Contains(string(raw), "do-not-index") {
		t.Fatal("unapproved fields leaked")
	}
	x := docs[0]
	x.TenantID = "another-tenant"
	if x.ID() == docs[0].ID() {
		t.Fatal("tenant collision")
	}
	x.TenantID, x.TaskID = "a:b", "c"
	y := x
	y.TenantID, y.TaskID = "a", "b:c"
	if x.ID() == y.ID() {
		t.Fatal("delimiter collision")
	}
}

func TestCanalRejectsBrokenFullImage(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing tenant":          func(r map[string]any) { delete(r, "tenant_id") },
		"missing nullable column": func(r map[string]any) { delete(r, "cancelled_reason") },
		"bad version":             func(r map[string]any) { r["status_version"] = "-1" },
		"overflow version":        func(r map[string]any) { r["status_version"] = "2147483648" },
		"unknown status":          func(r map[string]any) { r["status"] = "NEW_STATE" },
		"invalid timestamp":       func(r map[string]any) { r["created_at"] = "0000-00-00 00:00:00" },
		"invalid payload":         func(r map[string]any) { r["payload"] = `{"duration_seconds":1,"secret":"oops"}` },
		"numeric version":         func(r map[string]any) { r["status_version"] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			r := taskRow()
			mutate(r)
			if _, err := DecodeCanal(canalMessage(taskRow(), r), "meshops_course"); err == nil {
				t.Fatal("must reject entire message")
			}
		})
	}
}

func TestCanalRejectsUnsupportedEnvelope(t *testing.T) {
	for _, raw := range []string{
		`{"database":"other","table":"tasks","type":"INSERT","data":[]}`,
		`{"database":"meshops_course","table":"other","type":"INSERT","data":[]}`,
		`{"database":"meshops_course","table":"tasks","type":"DELETE","data":[]}`,
		`{"database":"meshops_course","table":"tasks","type":"ALTER","isDdl":true}`,
		`{"database":"meshops_course","table":"tasks","type":"UPDATE","data":[]}`,
		`null`, `{`, `{} {}`,
	} {
		if _, err := DecodeCanal([]byte(raw), "meshops_course"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCanalMySQLEnumOrdinal(t *testing.T) {
	r := taskRow()
	r["status"] = "2"
	var envelope map[string]any
	_ = json.Unmarshal(canalMessage(r), &envelope)
	envelope["mysqlType"] = map[string]string{"status": "enum('CREATED','DISPATCH_PENDING','DISPATCHED','ACKED','EXECUTING','SUCCEEDED','FAILED','CANCELLED','TIMED_OUT','REJECTED')"}
	raw, _ := json.Marshal(envelope)
	docs, err := DecodeCanal(raw, "meshops_course")
	if err != nil || docs[0].Status != "DISPATCH_PENDING" {
		t.Fatalf("enum ordinal %v %v", docs, err)
	}
	envelope["mysqlType"] = map[string]string{"status": "enum('DISPATCH_PENDING','CREATED')"}
	raw, _ = json.Marshal(envelope)
	if _, err = DecodeCanal(raw, "meshops_course"); err == nil {
		t.Fatal("schema enum order change silently accepted")
	}
}

func TestCanalIgnoresForeignDatabaseDDL(t *testing.T) {
	data := []byte(`{"data":null,"database":"search_snapshot_test_123","isDdl":true,"table":"","type":"QUERY","sql":"CREATE DATABASE search_snapshot_test_123"}`)
	docs, err := DecodeCanal(data, "meshops_course")
	if err != nil || len(docs) != 0 {
		t.Fatalf("foreign control event: %v %v", docs, err)
	}
	target := []byte(`{"data":null,"database":"meshops_course","isDdl":true,"table":"","type":"QUERY","sql":"DROP DATABASE meshops_course"}`)
	if _, err = DecodeCanal(target, "meshops_course"); err == nil {
		t.Fatal("task database DDL must halt projection")
	}
}
