package repository

import (
	"errors"
	"math"
	"regexp"
	"sync"

	commonv1 "example.com/meshops-course/gen/common/v1"
	"google.golang.org/protobuf/proto"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidID(id string) bool { return idPattern.MatchString(id) }

// Memory owns its map and all messages stored inside it.
type Memory struct {
	mu   sync.RWMutex
	rows map[string]*commonv1.EntitySnapshot
}

func New() *Memory {
	return &Memory{rows: make(map[string]*commonv1.EntitySnapshot)}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func Validate(id string, snapshot *commonv1.EntitySnapshot) error {
	if !ValidID(id) {
		return errors.New("entity_id must be 1..64 ASCII letters, digits, dot, underscore or hyphen; first character must be alphanumeric")
	}
	if snapshot == nil {
		return errors.New("snapshot is required")
	}
	if snapshot.EntityType != "person" && snapshot.EntityType != "drone" {
		return errors.New("entity_type must be person or drone")
	}
	loc := snapshot.Location
	if loc == nil {
		return errors.New("location is required")
	}
	if !finite(loc.Latitude) || loc.Latitude < -90 || loc.Latitude > 90 {
		return errors.New("latitude must be finite and within -90..90")
	}
	if !finite(loc.Longitude) || loc.Longitude < -180 || loc.Longitude > 180 {
		return errors.New("longitude must be finite and within -180..180")
	}
	if power := snapshot.Power; power != nil && power.BatteryPercent != nil {
		value := *power.BatteryPercent
		if !finite(value) || value < 0 || value > 100 {
			return errors.New("battery_percent must be finite and within 0..100")
		}
	}
	return nil
}

func (m *Memory) Put(id string, snapshot *commonv1.EntitySnapshot) error {
	if err := Validate(id, snapshot); err != nil {
		return err
	}
	// Clone prevents subsequent caller edits from changing our stored row.
	owned := proto.Clone(snapshot).(*commonv1.EntitySnapshot)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[id] = owned
	return nil
}

func (m *Memory) Get(id string) (*commonv1.EntitySnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, found := m.rows[id]
	if !found {
		return nil, false
	}
	// Readers receive their own deep copy, including nested optional fields.
	return proto.Clone(row).(*commonv1.EntitySnapshot), true
}
