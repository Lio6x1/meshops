package tasks

import (
	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInspectValidation(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"duration_seconds":0}`, `{"duration_seconds":61}`, `{"duration_seconds":1.5}`, `{"duration_seconds":1,"unknown":true}`, `{"duration_seconds":1,"duration_seconds":2}`, `{"duration_seconds":1,"note":null}`, `{"duration_seconds":1} {}`, `{"duration_seconds":1,"note":"` + strings.Repeat("界", 86) + `"}`} {
		if _, e := ParseInspect(raw); e == nil {
			t.Errorf("accepted invalid input %q", raw)
		}
	}
	p, e := ParseInspect(`{"note":"合法","duration_seconds":60}`)
	if e != nil || p.DurationSeconds != 60 || p.Note != "合法" {
		t.Fatalf("valid inspect rejected: %+v %v", p, e)
	}
}
func TestCreateHashUsesDeadlinePresenceNotClock(t *testing.T) {
	r := &taskv1.CreateTaskRequest{TaskType: "inspect", TargetEntityId: "drone-001", Payload: &commonv1.TaskPayload{PayloadJson: `{"duration_seconds":5}`}}
	_, h, _, e := normalizeCreate(r)
	if e != nil {
		t.Fatal(e)
	}
	r.Payload.PayloadJson = `{ "note":"", "duration_seconds":5 }`
	r.Priority = 5
	_, same, _, _ := normalizeCreate(r)
	if h != same {
		t.Fatal("equivalent requests have different digests")
	}
	r.Deadline = timestamppb.New(time.Date(2026, 9, 5, 0, 5, 0, 0, time.UTC))
	_, different, _, _ := normalizeCreate(r)
	if h == different {
		t.Fatal("explicit deadline presence missing from hash")
	}
}
func TestTaskTransitionAuthorityAndTerminalBarrier(t *testing.T) {
	for from := commonv1.TaskStatus_TASK_STATUS_SUCCEEDED; from <= commonv1.TaskStatus_TASK_STATUS_REJECTED; from++ {
		for to := commonv1.TaskStatus_TASK_STATUS_CREATED; to <= commonv1.TaskStatus_TASK_STATUS_REJECTED; to++ {
			for _, role := range []string{"executor", "dispatcher_service", "system", "operator"} {
				if CanTransition(from, to, true, role) {
					t.Fatalf("terminal state changed: %v -> %v by %s", from, to, role)
				}
			}
		}
	}
	if !CanTransition(commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING, commonv1.TaskStatus_TASK_STATUS_ACKED, false, "executor") {
		t.Fatal("ACK arriving before dispatcher status update must be legal")
	}
	if CanTransition(commonv1.TaskStatus_TASK_STATUS_EXECUTING, commonv1.TaskStatus_TASK_STATUS_DISPATCHED, false, "dispatcher_service") {
		t.Fatal("DISPATCHED must not regress execution")
	}
	if CanTransition(commonv1.TaskStatus_TASK_STATUS_EXECUTING, commonv1.TaskStatus_TASK_STATUS_CANCELLED, false, "executor") {
		t.Fatal("unsolicited cancel accepted")
	}
	if !CanTransition(commonv1.TaskStatus_TASK_STATUS_EXECUTING, commonv1.TaskStatus_TASK_STATUS_CANCELLED, true, "executor") {
		t.Fatal("requested cancel rejected")
	}
}
func TestTaskCursorBoundToTenantFilterAndPosition(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	now := time.Now().UTC()
	c := cursor{V: 1, Kind: "tasks", Tenant: "tenant", Filter: "filter", LastTime: now.Add(-time.Second), LastID: "task-001", Upper: now, Expires: now.Add(time.Minute)}
	token := seal(c, key)
	if _, e := unseal(token, key, "tenant", "filter"); e != nil {
		t.Fatal(e)
	}
	for _, x := range []struct{ token, tenant, filter string }{{token, "other", "filter"}, {token, "tenant", "other"}, {token[:len(token)-2] + "xx", "tenant", "filter"}, {strings.Repeat("x", 2049), "tenant", "filter"}} {
		if _, e := unseal(x.token, key, x.tenant, x.filter); e == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	c.Expires = now.Add(-time.Second)
	if _, e := unseal(seal(c, key), key, "tenant", "filter"); e == nil {
		t.Fatal("expired cursor accepted")
	}
	c.Expires = now.Add(time.Minute)
	c.LastTime = now.Add(time.Second)
	if _, e := unseal(seal(c, key), key, "tenant", "filter"); e == nil {
		t.Fatal("position beyond upper bound accepted")
	}
}
func TestHistoricalMigrationsRemainExecutable(t *testing.T) {
	for _, name := range []string{"001_initial_schema.sql", "002_framework_contracts.sql", "003_implementation_contracts.sql"} {
		raw, e := os.ReadFile("../../migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		statements, e := SplitSQL(string(raw))
		if e != nil {
			t.Fatal(e)
		}
		if len(statements) == 0 {
			t.Fatal("empty migration")
		}
		for _, s := range statements {
			if strings.HasPrefix(s, "DELIMITER") {
				t.Fatal("mysql client directive leaked")
			}
		}
		if strings.HasPrefix(name, "001") && len(statements) != 11 {
			t.Fatalf("expected 8 tables, seed and two events; got %d", len(statements))
		}
	}
}
