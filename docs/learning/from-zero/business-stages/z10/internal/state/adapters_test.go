package state

import (
	"bytes"
	"example.com/meshops-course/internal/platform"
	"os"
	"testing"
	"time"
)

func TestSixAdapters(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 1, 0, time.UTC)
	for _, kind := range []string{"person", "drone", "vehicle", "robot", "sensor", "facility"} {
		t.Run(kind, func(t *testing.T) {
			src := platform.Source{TenantID: "demo_tenant", ID: kind + "_sim", Adapter: kind, Generation: 1, StaleAfter: 30 * time.Second, Entities: map[string]string{kind + "-001": kind + "-001"}}
			raw, err := os.ReadFile("../../testdata/sources/" + kind + ".json")
			if err != nil {
				t.Fatal(err)
			}
			e, err := Normalize(raw, src, now)
			if err != nil {
				t.Fatal(err)
			}
			if e.Snapshot.EntityType != kind || e.EntityVersion != 1 || !e.ReceivedAt.AsTime().Equal(now) {
				t.Fatal(e)
			}
			if kind == "vehicle" && e.Snapshot.Velocity.Speed != 10 {
				t.Fatal("unit conversion")
			}
			if kind == "robot" && e.Snapshot.Power.GetBatteryPercent() != 65 {
				t.Fatal("battery conversion")
			}
			if kind == "person" && e.Snapshot.Power != nil {
				t.Fatal("person acquired power")
			}
			if _, err = Normalize(bytes.Replace(raw, []byte("{\n"), []byte("{\n\"unknown\":1,"), 1), src, now); err == nil {
				t.Fatal("accepted unknown field")
			}
		})
	}
}

func TestStrictPerson(t *testing.T) {
	src := platform.Source{TenantID: "t", ID: "s", Adapter: "person", Generation: 1, StaleAfter: 30 * time.Second, Entities: map[string]string{"p": "p"}}
	good := `{"employee_id":"p","observation_id":"e","version":1,"observed_at":"2026-09-05T00:00:00Z","on_duty":false,"availability":"idle","skills":[]}`
	for _, bad := range []string{
		string(bytes.ReplaceAll([]byte(good), []byte(`"version":1`), []byte(`"version":1.5`))),
		string(bytes.ReplaceAll([]byte(good), []byte(`"on_duty":false,`), nil)),
		string(bytes.ReplaceAll([]byte(good), []byte(`"version":1`), []byte(`"version":-1`))),
		good[:len(good)-1] + `,"latitude":0}`,
		good + `{}`,
	} {
		if _, err := Normalize([]byte(bad), src, time.Now()); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if _, err := Normalize([]byte(good), src, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestDroneCoordinatesAndFacilityCapacityAreValidated(t *testing.T) {
	for _, item := range []struct{ kind, old, replacement string }{
		{"drone", `"lat_e7": 312300000`, `"lat_e7": 910000000`},
		{"drone", `"lon_e7": 1214700000`, `"lon_e7": 1810000000`},
		{"facility", `"occupied": 2`, `"occupied": 9`},
	} {
		t.Run(item.kind+item.replacement, func(t *testing.T) {
			raw, err := os.ReadFile("../../testdata/sources/" + item.kind + ".json")
			if err != nil {
				t.Fatal(err)
			}
			changed := bytes.Replace(raw, []byte(item.old), []byte(item.replacement), 1)
			if bytes.Equal(raw, changed) {
				t.Fatal("test did not change fixture")
			}
			source := platform.Source{TenantID: "t", ID: "s", Adapter: item.kind, Generation: 1, StaleAfter: 30 * time.Second, Entities: map[string]string{item.kind + "-001": item.kind + "-001"}}
			if _, err = Normalize(changed, source, time.Now()); err == nil {
				t.Fatal("invalid domain value accepted")
			}
		})
	}
}
