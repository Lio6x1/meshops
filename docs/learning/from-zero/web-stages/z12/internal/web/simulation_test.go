package web

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/simulation"
	"fmt"
	"testing"
)

type simulationStore struct {
	mode  simulation.Mode
	fail  bool
	count int
}

func (p *simulationStore) SetCount(_ context.Context, tenant, source string, count int) error {
	if p.fail {
		return errors.New("private redis endpoint")
	}
	p.count = count
	return nil
}

func (p *simulationStore) SetDesired(_ context.Context, tenant, source string, mode simulation.Mode) error {
	if p.fail {
		return errors.New("private redis endpoint")
	}
	p.mode = mode
	return nil
}
func (p *simulationStore) Read(_ context.Context, tenant, source string) (simulation.Status, error) {
	if p.fail {
		return simulation.Status{}, errors.New("private redis endpoint")
	}
	return simulation.Status{Desired: p.mode, Count: p.count}, nil
}
func TestSimulationCountBoundsAndActiveInventory(t *testing.T) {
	s, _ := fixture(t)
	control := &simulationStore{mode: simulation.Running, count: 1}
	s.cfg.Simulation = control
	ids := map[string]string{}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("drone-%03d", i)
		ids[id] = id
		s.cfg.Registry.Bindings[platform.Key("demo_tenant", id)] = platform.Binding{TenantID: "demo_tenant", EntityID: id, SourceID: "drone_sim", Type: "drone", ExecutorID: "aircraft", Tasks: []string{"inspect"}}
	}
	s.cfg.Registry.Sources = map[string]platform.Source{"demo": {ID: "drone_sim", TenantID: "demo_tenant", Adapter: "drone", Entities: ids}}
	c, csrf := login(t, s, "operator")
	for _, body := range []string{`{}`, `{"count":null}`, `{"count":6}`, `{"count":-1}`, `{"count":1.5}`, `{"count":2,"extra":1}`, `{"count":2} {}`} {
		if w := request(s, "PUT", "/api/v1/simulation/drone_sim/count", body, c, csrf, "http://localhost:18090"); w.Code != 400 {
			t.Fatalf("body %s code %d", body, w.Code)
		}
	}
	if w := request(s, "PUT", "/api/v1/simulation/drone_sim/count", `{"count":5}`, c, "", "http://localhost:18090"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(s, "PUT", "/api/v1/simulation/secret/count", `{"count":5}`, c, csrf, "http://localhost:18090"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, n := range []int{5, 2, 0, 1} {
		w := request(s, "PUT", "/api/v1/simulation/drone_sim/count", fmt.Sprintf(`{"count":%d}`, n), c, csrf, "http://localhost:18090")
		if w.Code != 202 || control.mode != simulation.Running {
			t.Fatalf("count %d: %d %s", n, w.Code, w.Body.String())
		}
		w = request(s, "GET", "/api/v1/entities", "", c, "", "")
		var data struct {
			Entities []struct {
				EntityID string `json:"entityId"`
			}
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data.Entities) != n {
			t.Fatalf("inventory %d: %s", n, w.Body.String())
		}
		if n > 0 && data.Entities[0].EntityID != "drone-001" {
			t.Fatal("not deterministic first N")
		}
	}
	if len(s.cfg.Registry.Bindings) != 6 {
		t.Fatal("shrink deleted registered identities")
	}
	control.fail = true
	if w := request(s, "GET", "/api/v1/entities", "", c, "", ""); w.Code != 503 {
		t.Fatal("failed control exposed all candidate entities", w.Code)
	}
}
func TestSimulationControlIsAuthenticatedScopedAndReportsDesiredNotApplied(t *testing.T) {
	s, _ := fixture(t)
	p := &simulationStore{mode: simulation.Running}
	s.cfg.Simulation = p
	s.cfg.Registry.Sources = map[string]platform.Source{
		"demo":  {ID: "drone_sim", TenantID: "demo_tenant", Adapter: "drone", Entities: map[string]string{"drone-001": "drone-001"}},
		"other": {ID: "secret", TenantID: "other", Adapter: "person"},
	}
	if w := request(s, "GET", "/api/v1/simulation", "", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	c, csrf := login(t, s, "operator")
	if w := request(s, "PUT", "/api/v1/simulation/drone_sim", `{"mode":"paused"}`, c, "", "http://localhost:18090"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"mode":"invalid"}`, `{"mode":"paused","extra":true}`, `{"mode":"paused"} {}`} {
		if w := request(s, "PUT", "/api/v1/simulation/drone_sim", body, c, csrf, "http://localhost:18090"); w.Code != 400 {
			t.Fatalf("%s: %d", body, w.Code)
		}
	}
	if w := request(s, "PUT", "/api/v1/simulation/secret", `{"mode":"paused"}`, c, csrf, "http://localhost:18090"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := request(s, "PUT", "/api/v1/simulation/drone_sim", `{"mode":"paused"}`, c, csrf, "http://localhost:18090")
	if w.Code != 202 || p.mode != simulation.Paused {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	p.fail = true
	if w := request(s, "GET", "/api/v1/simulation", "", c, "", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestSimulationDisabledIsExplicit(t *testing.T) {
	s, _ := fixture(t)
	c, _ := login(t, s, "operator")
	if w := request(s, "GET", "/api/v1/simulation", "", c, "", ""); w.Code != 503 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
