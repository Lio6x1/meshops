package web

import (
	"context"
	"errors"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/simulation"
	"testing"
)

type simulationStore struct {
	mode simulation.Mode
	fail bool
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
	return simulation.Status{Desired: p.mode}, nil
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
