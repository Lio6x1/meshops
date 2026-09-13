// Package state 实现课程参考实现中的遥测链路。
package state

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"math"
	"regexp"
	"sort"
	"time"
)

const MaxVersion int64 = platform.MaxEntityVersion

var idPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

func validID(s string, max int) bool { return len(s) > 0 && len(s) <= max && idPattern.MatchString(s) }
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func ordered(v []string) []string {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// object 拒绝各层级的未知键，并保留 JSON 数字的语法形式。
type object map[string]json.RawMessage

func decodeObject(raw []byte, allowed ...string) (object, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("object required")
	}
	m := object{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || !oneOf(key, allowed...) {
			return nil, fmt.Errorf("unknown object field")
		}
		if _, exists := m[key]; exists {
			return nil, fmt.Errorf("duplicate field %s", key)
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, err
		}
		m[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	for k := range m {
		if !oneOf(k, allowed...) {
			return nil, fmt.Errorf("unknown field %s", k)
		}
	}
	return m, nil
}
func field[T any](m object, k string, required bool) (T, error) {
	var v T
	b, ok := m[k]
	if !ok {
		if required {
			return v, fmt.Errorf("missing %s", k)
		}
		return v, nil
	}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return v, fmt.Errorf("null %s", k)
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("%s: %w", k, err)
	}
	return v, nil
}
func location(m object, latKey, lonKey, altKey string, scale, altScale float64) (*commonv1.Location, error) {
	_, a := m[latKey]
	_, b := m[lonKey]
	if !a && !b {
		if altKey != "" {
			if _, ok := m[altKey]; ok {
				return nil, fmt.Errorf("altitude without location")
			}
		}
		return nil, nil
	}
	if a != b {
		return nil, fmt.Errorf("coordinates must be paired")
	}
	lat, err := field[float64](m, latKey, true)
	if err != nil {
		return nil, err
	}
	lon, err := field[float64](m, lonKey, true)
	if err != nil {
		return nil, err
	}
	lat /= scale
	lon /= scale
	if !finite(lat) || !finite(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return nil, fmt.Errorf("invalid WGS84 location")
	}
	l := &commonv1.Location{Latitude: lat, Longitude: lon, CoordinateSystem: "WGS84"}
	if altKey != "" {
		if _, ok := m[altKey]; ok {
			v, err := field[float64](m, altKey, true)
			if err != nil || !finite(v) {
				return nil, fmt.Errorf("invalid altitude")
			}
			v /= altScale
			l.Altitude = &v
		}
	}
	return l, nil
}
func nestedLocation(m object, key, lat, lon string) (*commonv1.Location, error) {
	b, ok := m[key]
	if !ok {
		return nil, nil
	}
	n, err := decodeObject(b, lat, lon)
	if err != nil {
		return nil, err
	}
	if len(n) != 2 {
		return nil, fmt.Errorf("%s requires both coordinates", key)
	}
	return location(n, lat, lon, "", 1, 1)
}

// Normalize 只接受文档约定的六种传输格式。能力来自
// 接入时的可信注册表；来源自报的任务不能授予能力。
func Normalize(raw []byte, source platform.Source, received time.Time) (*commonv1.EntityStateEvent, error) {
	keys := map[string][]string{
		"person":   {"employee_id", "observation_id", "version", "observed_at", "latitude", "longitude", "on_duty", "availability", "skills"},
		"drone":    {"aircraft_id", "sample_id", "revision", "timestamp_ms", "lat_e7", "lon_e7", "altitude_cm", "battery_pct", "flight_state", "supported_tasks"},
		"vehicle":  {"vehicle_id", "event_id", "sequence", "recorded_at", "position", "speed_kmh", "load_kg", "availability", "battery_pct"},
		"robot":    {"robot_id", "event_id", "revision", "timestamp", "pose", "energy", "fault_codes", "availability"},
		"sensor":   {"sensor_id", "event_id", "version", "measured_at", "measurement", "health", "site"},
		"facility": {"facility_id", "event_id", "revision", "observed_at", "coordinates", "kind", "open", "slots"},
	}
	allowed, ok := keys[source.Adapter]
	if !ok {
		return nil, fmt.Errorf("unsupported adapter")
	}
	m, err := decodeObject(raw, allowed...)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{"person": "employee_id", "drone": "aircraft_id", "vehicle": "vehicle_id", "robot": "robot_id", "sensor": "sensor_id", "facility": "facility_id"}
	versions := map[string]string{"person": "version", "drone": "revision", "vehicle": "sequence", "robot": "revision", "sensor": "version", "facility": "revision"}
	times := map[string]string{"person": "observed_at", "drone": "timestamp_ms", "vehicle": "recorded_at", "robot": "timestamp", "sensor": "measured_at", "facility": "observed_at"}
	rawID, err := field[string](m, ids[source.Adapter], true)
	if err != nil {
		return nil, err
	}
	entity, ok := source.Entities[rawID]
	if !ok {
		return nil, fmt.Errorf("unregistered raw entity ID")
	}
	version, err := field[int64](m, versions[source.Adapter], true)
	if err != nil || version < 1 || version > MaxVersion {
		return nil, fmt.Errorf("invalid integer entity version")
	}
	eventKey := "event_id"
	if source.Adapter == "person" {
		eventKey = "observation_id"
	}
	if source.Adapter == "drone" {
		eventKey = "sample_id"
	}
	eventID, err := field[string](m, eventKey, true)
	if err != nil || !validID(eventID, 128) {
		return nil, fmt.Errorf("invalid event ID")
	}
	var occurred time.Time
	if source.Adapter == "drone" {
		ms, e := field[int64](m, times[source.Adapter], true)
		if e != nil {
			return nil, e
		}
		occurred = time.UnixMilli(ms).UTC()
	} else {
		s, e := field[string](m, times[source.Adapter], true)
		if e != nil {
			return nil, e
		}
		occurred, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fmt.Errorf("invalid occurrence time")
		}
		occurred = occurred.UTC()
	}
	snap := &commonv1.EntitySnapshot{EntityType: source.Adapter}
	switch source.Adapter {
	case "person":
		duty, e := field[bool](m, "on_duty", true)
		if e != nil {
			return nil, e
		}
		a, e := field[string](m, "availability", true)
		if e != nil || !oneOf(a, "idle", "busy", "offline") {
			return nil, fmt.Errorf("invalid availability")
		}
		skills, e := field[[]string](m, "skills", true)
		if e != nil {
			return nil, e
		}
		snap.Person = &commonv1.PersonState{OnDuty: duty, Availability: a, Skills: ordered(skills)}
		snap.Status = a
		snap.Location, err = location(m, "latitude", "longitude", "", 1, 1)
	case "drone":
		for _, name := range []string{"lat_e7", "lon_e7", "altitude_cm"} {
			if _, err := field[int64](m, name, false); err != nil {
				return nil, err
			}
		}
		bat, e := field[float64](m, "battery_pct", true)
		if e != nil || !finite(bat) || bat < 0 || bat > 100 {
			return nil, fmt.Errorf("invalid battery")
		}
		s, e := field[string](m, "flight_state", true)
		if e != nil || !oneOf(s, "idle", "flying", "returning", "fault") {
			return nil, fmt.Errorf("invalid flight state")
		}
		if _, e = field[[]string](m, "supported_tasks", false); e != nil {
			return nil, e
		}
		snap.Power = &commonv1.PowerState{BatteryPercent: &bat}
		snap.Status = s
		snap.Location, err = location(m, "lat_e7", "lon_e7", "altitude_cm", 1e7, 100)
	case "vehicle":
		speed, e := field[float64](m, "speed_kmh", true)
		if e != nil || !finite(speed) || speed < 0 {
			return nil, fmt.Errorf("invalid speed")
		}
		load, e := field[float64](m, "load_kg", true)
		if e != nil || !finite(load) || load < 0 {
			return nil, fmt.Errorf("invalid load")
		}
		a, e := field[string](m, "availability", true)
		if e != nil || !oneOf(a, "idle", "busy", "offline") {
			return nil, fmt.Errorf("invalid availability")
		}
		snap.Velocity = &commonv1.Velocity{Speed: speed / 3.6}
		snap.Vehicle = &commonv1.VehicleState{LoadKg: &load, Availability: a}
		snap.Status = a
		snap.Location, err = nestedLocation(m, "position", "lat", "lng")
		if _, ok := m["battery_pct"]; ok {
			v, e := field[float64](m, "battery_pct", true)
			if e != nil || !finite(v) || v < 0 || v > 100 {
				return nil, fmt.Errorf("invalid battery")
			}
			snap.Power = &commonv1.PowerState{BatteryPercent: &v}
		}
	case "robot":
		energy, e := decodeObject(m["energy"], "ratio")
		if e != nil {
			return nil, e
		}
		ratio, e := field[float64](energy, "ratio", true)
		if e != nil || !finite(ratio) || ratio < 0 || ratio > 1 {
			return nil, fmt.Errorf("invalid energy ratio")
		}
		v := ratio * 100
		codes, e := field[[]string](m, "fault_codes", true)
		if e != nil {
			return nil, e
		}
		a, e := field[string](m, "availability", true)
		if e != nil || !oneOf(a, "idle", "busy", "offline") {
			return nil, fmt.Errorf("invalid availability")
		}
		snap.Power = &commonv1.PowerState{BatteryPercent: &v}
		snap.Robot = &commonv1.RobotState{FaultCodes: ordered(codes), Availability: a}
		snap.Status = a
		snap.Location, err = nestedLocation(m, "pose", "latitude", "longitude")
	case "sensor":
		measure, e := decodeObject(m["measurement"], "value", "unit")
		if e != nil {
			return nil, e
		}
		v, e := field[float64](measure, "value", true)
		if e != nil || !finite(v) {
			return nil, fmt.Errorf("invalid reading")
		}
		unit, e := field[string](measure, "unit", true)
		if e != nil || unit == "" {
			return nil, fmt.Errorf("invalid unit")
		}
		health, e := field[string](m, "health", true)
		if e != nil || !oneOf(health, "ok", "warn", "fault") {
			return nil, fmt.Errorf("invalid health")
		}
		snap.Sensor = &commonv1.SensorState{Reading: &v, Unit: unit, Health: health, MeasuredAt: timestamppb.New(occurred)}
		snap.Status = "online"
		snap.Location, err = nestedLocation(m, "site", "lat", "lon")
	case "facility":
		slots, e := decodeObject(m["slots"], "occupied", "total")
		if e != nil {
			return nil, e
		}
		used, e := field[uint32](slots, "occupied", true)
		if e != nil {
			return nil, e
		}
		total, e := field[uint32](slots, "total", true)
		if e != nil || used > total {
			return nil, fmt.Errorf("invalid slots")
		}
		kind, e := field[string](m, "kind", true)
		if e != nil || kind != "charging_station" {
			return nil, fmt.Errorf("invalid facility kind")
		}
		open, e := field[bool](m, "open", true)
		if e != nil {
			return nil, e
		}
		snap.Facility = &commonv1.FacilityState{FacilityType: kind, IsOpen: open, OccupiedSlots: used, TotalSlots: total}
		snap.Status = "closed"
		if open {
			snap.Status = "open"
		}
		snap.Location, err = nestedLocation(m, "coordinates", "latitude", "longitude")
	}
	if err != nil {
		return nil, err
	}
	stale := source.StaleAfter
	if stale <= 0 {
		stale = 30 * time.Second
	}
	event := &commonv1.EntityStateEvent{EventId: eventID, TenantId: source.TenantID, EntityId: entity, EntityVersion: version, SourceId: source.ID, SourceGeneration: source.Generation, SchemaVersion: 1, Operation: commonv1.EntityOperation_ENTITY_OPERATION_UPSERT, OccurredAt: timestamppb.New(occurred), ReceivedAt: timestamppb.New(received.UTC()), ExpiresAt: timestamppb.New(occurred.Add(stale)), Snapshot: snap}
	if err := validateShape(event); err != nil {
		return nil, err
	}
	return event, nil
}

func GenerateRaw(source platform.Source, rawID string, version int64, now time.Time) ([]byte, error) {
	if _, ok := source.Entities[rawID]; !ok || version < 1 || version > MaxVersion {
		return nil, fmt.Errorf("unregistered ID or invalid version")
	}
	generator, err := NewRawGenerator(source)
	if err != nil {
		return nil, err
	}
	return generator.Generate(rawID, version, now)
}
