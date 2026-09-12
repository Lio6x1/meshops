package repository

import (
	"fmt"
	"math"
	"sync"
	"testing"

	commonv1 "example.com/meshops-course/gen/common/v1"
)

func sample(kind string, battery *float64) *commonv1.EntitySnapshot {
	row := &commonv1.EntitySnapshot{EntityType: kind, Location: &commonv1.Location{Latitude: 31.2304, Longitude: 121.4737}}
	if battery != nil {
		row.Power = &commonv1.PowerState{BatteryPercent: battery}
	}
	return row
}

func TestPutGetAndClone(t *testing.T) {
	m := New()
	if row, found := m.Get("missing"); found || row != nil {
		t.Fatal("unexpected missing row")
	}
	power := 0.0
	input := sample("drone", &power)
	if err := m.Put("drone-001", input); err != nil {
		t.Fatal(err)
	}
	input.Location.Latitude = 55
	*input.Power.BatteryPercent = 99
	row, found := m.Get("drone-001")
	if !found || row.Location.Latitude != 31.2304 || *row.Power.BatteryPercent != 0 {
		t.Fatal("input alias leaked")
	}
	row.Location.Longitude = 66
	*row.Power.BatteryPercent = 33
	again, _ := m.Get("drone-001")
	if again.Location.Longitude != 121.4737 || *again.Power.BatteryPercent != 0 {
		t.Fatal("output alias leaked")
	}
	if err := m.Put("person-001", sample("person", nil)); err != nil {
		t.Fatal(err)
	}
	person, _ := m.Get("person-001")
	if person.Power != nil {
		t.Fatal("unknown power must stay absent")
	}
}

func TestInvalidInputsDoNotOverwrite(t *testing.T) {
	tests := []struct {
		name string
		id   string
		edit func(*commonv1.EntitySnapshot)
	}{
		{"empty ID", "", func(s *commonv1.EntitySnapshot) {}},
		{"space ID", "bad id", func(s *commonv1.EntitySnapshot) {}},
		{"unknown type", "person-001", func(s *commonv1.EntitySnapshot) { s.EntityType = "car" }},
		{"missing location", "person-001", func(s *commonv1.EntitySnapshot) { s.Location = nil }},
		{"latitude range", "person-001", func(s *commonv1.EntitySnapshot) { s.Location.Latitude = 91 }},
		{"longitude range", "person-001", func(s *commonv1.EntitySnapshot) { s.Location.Longitude = -181 }},
		{"NaN", "person-001", func(s *commonv1.EntitySnapshot) { s.Location.Latitude = math.NaN() }},
		{"infinity", "person-001", func(s *commonv1.EntitySnapshot) { s.Location.Longitude = math.Inf(1) }},
		{"battery range", "person-001", func(s *commonv1.EntitySnapshot) { v := 101.0; s.Power = &commonv1.PowerState{BatteryPercent: &v} }},
		{"battery NaN", "person-001", func(s *commonv1.EntitySnapshot) { v := math.NaN(); s.Power = &commonv1.PowerState{BatteryPercent: &v} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			if err := m.Put("person-001", sample("person", nil)); err != nil {
				t.Fatal(err)
			}
			row := sample("person", nil)
			tt.edit(row)
			if err := m.Put(tt.id, row); err == nil {
				t.Fatal("expected validation error")
			}
			stored, _ := m.Get("person-001")
			if stored.Location.Latitude != 31.2304 || stored.Power != nil {
				t.Fatal("invalid request changed existing row")
			}
		})
	}
	if err := New().Put("person-001", nil); err == nil {
		t.Fatal("nil snapshot accepted")
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				id := fmt.Sprintf("drone-%d", worker%4)
				if err := m.Put(id, sample("drone", nil)); err != nil {
					t.Error(err)
					return
				}
				row, found := m.Get(id)
				if !found || row.EntityType != "drone" {
					t.Error("missing concurrent row")
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestNewRepositoryLosesPreviousProcessState(t *testing.T) {
	before := New()
	if err := before.Put("person-001", sample("person", nil)); err != nil {
		t.Fatal(err)
	}
	after := New()
	if _, found := after.Get("person-001"); found {
		t.Fatal("memory must not imply persistence")
	}
}
