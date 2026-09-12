package edge

import (
	commonv1 "example.com/meshops-course/gen/common/v1"
	"math"
	"math/rand"
)

// simulateMotion adds bounded, reproducible movement and battery variation to
// the normalized fixture. It never invents missing components or capabilities.
// Observation clocks and event identities intentionally remain real/fresh.
func simulateMotion(event *commonv1.EntityStateEvent, rng *rand.Rand) {
	if event == nil || event.Snapshot == nil {
		return
	}
	s := event.Snapshot
	if s.EntityType == "sensor" || s.EntityType == "facility" {
		return
	}
	if s.Location != nil {
		s.Location.Latitude = math.Max(-90, math.Min(90, s.Location.Latitude+(rng.Float64()-.5)*.00002))
		s.Location.Longitude = math.Max(-180, math.Min(180, s.Location.Longitude+(rng.Float64()-.5)*.00002))
	}
	if s.Power != nil && s.Power.BatteryPercent != nil {
		battery := math.Max(0, math.Min(100, *s.Power.BatteryPercent+(rng.Float64()-.5)*2))
		s.Power.BatteryPercent = &battery
	}
}
