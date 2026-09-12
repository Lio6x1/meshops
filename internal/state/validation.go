package state

import (
	"crypto/sha256"
	"encoding/hex"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"math"
	"reflect"
	"time"
)

func validateShape(e *commonv1.EntityStateEvent) error {
	if e == nil {
		return fmt.Errorf("missing event")
	}
	if !validID(e.TenantId, 64) || !validID(e.SourceId, 64) || !validID(e.EntityId, 128) || !validID(e.EventId, 128) {
		return fmt.Errorf("invalid identity")
	}
	if e.EntityVersion < 1 || e.EntityVersion > MaxVersion || e.SourceGeneration < 1 || e.SourceGeneration > MaxVersion {
		return fmt.Errorf("invalid version or generation")
	}
	if e.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if e.OccurredAt == nil || e.ExpiresAt == nil || e.ReceivedAt == nil || e.OccurredAt.CheckValid() != nil || e.ExpiresAt.CheckValid() != nil || e.ReceivedAt.CheckValid() != nil {
		return fmt.Errorf("valid occurrence, expiry and receipt times required")
	}
	if !e.ExpiresAt.AsTime().After(e.OccurredAt.AsTime()) {
		return fmt.Errorf("expiry must follow occurrence")
	}
	if e.Operation == commonv1.EntityOperation_ENTITY_OPERATION_DELETE {
		if e.Snapshot != nil {
			return fmt.Errorf("delete cannot carry snapshot")
		}
		return nil
	}
	if e.Operation != commonv1.EntityOperation_ENTITY_OPERATION_UPSERT || e.Snapshot == nil {
		return fmt.Errorf("upsert snapshot required")
	}
	s := e.Snapshot
	if s.Location != nil {
		l := s.Location
		if !finite(l.Latitude) || !finite(l.Longitude) || math.Abs(l.Latitude) > 90 || math.Abs(l.Longitude) > 180 || l.CoordinateSystem != "WGS84" {
			return fmt.Errorf("invalid location")
		}
		if l.Altitude != nil && !finite(*l.Altitude) || l.Accuracy != nil && (!finite(*l.Accuracy) || *l.Accuracy < 0) {
			return fmt.Errorf("invalid location optional values")
		}
	}
	if s.Velocity != nil {
		v := s.Velocity
		if !finite(v.Speed) || v.Speed < 0 || v.Heading != nil && (!finite(*v.Heading) || *v.Heading < 0 || *v.Heading >= 360) || v.VerticalSpeed != nil && !finite(*v.VerticalSpeed) {
			return fmt.Errorf("invalid velocity")
		}
	}
	if s.Power != nil {
		v := s.Power.BatteryPercent
		if v == nil || !finite(*v) || *v < 0 || *v > 100 {
			return fmt.Errorf("invalid battery")
		}
	}
	componentCount := 0
	for _, present := range []bool{s.Person != nil, s.Vehicle != nil, s.Robot != nil, s.Sensor != nil, s.Facility != nil} {
		if present {
			componentCount++
		}
	}
	switch s.EntityType {
	case "person":
		if s.Person == nil || s.Power != nil || s.Velocity != nil || componentCount != 1 || !oneOf(s.Person.Availability, "idle", "busy", "offline") || s.Status != s.Person.Availability || !reflect.DeepEqual(s.Person.Skills, ordered(s.Person.Skills)) && len(s.Person.Skills) > 0 {
			return fmt.Errorf("invalid person components")
		}
	case "drone":
		if s.Power == nil || componentCount != 0 || s.Velocity != nil || !oneOf(s.Status, "idle", "flying", "returning", "fault") {
			return fmt.Errorf("invalid drone components")
		}
	case "vehicle":
		if s.Vehicle == nil || s.Velocity == nil || componentCount != 1 || s.Vehicle.LoadKg == nil || !finite(s.Vehicle.GetLoadKg()) || s.Vehicle.GetLoadKg() < 0 || !oneOf(s.Vehicle.Availability, "idle", "busy", "offline") || s.Status != s.Vehicle.Availability {
			return fmt.Errorf("invalid vehicle components")
		}
	case "robot":
		if s.Robot == nil || s.Power == nil || s.Velocity != nil || componentCount != 1 || !oneOf(s.Robot.Availability, "idle", "busy", "offline") || s.Status != s.Robot.Availability || !reflect.DeepEqual(s.Robot.FaultCodes, ordered(s.Robot.FaultCodes)) && len(s.Robot.FaultCodes) > 0 {
			return fmt.Errorf("invalid robot components")
		}
	case "sensor":
		if s.Sensor == nil || s.Power != nil || s.Velocity != nil || componentCount != 1 || s.Sensor.Reading == nil || !finite(s.Sensor.GetReading()) || s.Sensor.Unit == "" || !oneOf(s.Sensor.Health, "ok", "warn", "fault") || s.Status != "online" || s.Sensor.MeasuredAt == nil || s.Sensor.MeasuredAt.CheckValid() != nil {
			return fmt.Errorf("invalid sensor components")
		}
	case "facility":
		if s.Facility == nil || s.Power != nil || s.Velocity != nil || componentCount != 1 || s.Facility.FacilityType != "charging_station" || s.Facility.OccupiedSlots > s.Facility.TotalSlots || s.Facility.IsOpen && s.Status != "open" || !s.Facility.IsOpen && s.Status != "closed" {
			return fmt.Errorf("invalid facility components")
		}
	default:
		return fmt.Errorf("unknown entity type")
	}
	if len(s.Metadata) > 0 {
		return fmt.Errorf("metadata is not supported")
	}
	return nil
}

// Validate checks authoritative ownership even for DELETE. It never accepts
// capability increases from telemetry. Ingest generates the final catalog.
func Validate(e *commonv1.EntityStateEvent, registry *platform.Registry) error {
	if err := validateShape(e); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	b, ok := registry.Lookup(e.TenantId, e.EntityId)
	if !ok || b.SourceID != e.SourceId || b.SourceGeneration != e.SourceGeneration {
		return status.Error(codes.PermissionDenied, "entity source binding mismatch")
	}
	src, ok := registry.GetSource(e.TenantId, e.SourceId)
	if !ok {
		return status.Error(codes.PermissionDenied, "source not registered")
	}
	stale := src.StaleAfter
	if stale <= 0 {
		stale = 30 * time.Second
	}
	if !e.ExpiresAt.AsTime().Equal(e.OccurredAt.AsTime().Add(stale)) {
		return status.Error(codes.InvalidArgument, "expiry does not match source policy")
	}
	if e.Snapshot != nil && e.Snapshot.EntityType != b.Type {
		return status.Error(codes.PermissionDenied, "registered type mismatch")
	}
	return nil
}
func authoritativeCatalog(e *commonv1.EntityStateEvent, r *platform.Registry) {
	if e.Snapshot == nil {
		return
	}
	b, _ := r.Lookup(e.TenantId, e.EntityId)
	e.Snapshot.Capability = nil
	e.Snapshot.TaskCatalog = nil
	if b.ExecutorID != "" {
		for _, task := range b.Tasks {
			if task == "inspect" {
				e.Snapshot.Capability = &commonv1.Capability{SupportedTasks: []string{"inspect"}}
				e.Snapshot.TaskCatalog = &commonv1.TaskCatalog{Definitions: []*commonv1.TaskDefinition{{TaskType: "inspect", Description: "Simulated inspection", ParameterSchemaJson: `{"type":"object","required":["duration_seconds"],"properties":{"duration_seconds":{"type":"integer","minimum":1,"maximum":60},"note":{"type":"string","maxLength":256}},"additionalProperties":false}`}}}
				break
			}
		}
	}
}
func contentHash(e *commonv1.EntityStateEvent) (string, error) {
	c := proto.Clone(e).(*commonv1.EntityStateEvent)
	c.ReceivedAt = nil
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(c)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
