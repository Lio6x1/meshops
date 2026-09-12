package edge

import (
	commonv1 "example.com/meshops-course/gen/common/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestDemoMotionUsesTimeAndVisibleDisplacement(t *testing.T) {
	for _, kind := range []string{"person", "drone", "vehicle", "robot", "sensor", "facility"} {
		makeEvent := func(seconds int64, version int64) *commonv1.EntityStateEvent {
			e := &commonv1.EntityStateEvent{EntityId: kind + "-001", SourceId: kind + "_sim", EntityVersion: version, OccurredAt: timestamppb.New(time.Unix(seconds, 0)), Snapshot: &commonv1.EntitySnapshot{EntityType: kind, Location: &commonv1.Location{Latitude: 31.23, Longitude: 121.47}}}
			simulateMotion(e, rand.New(rand.NewSource(1)))
			return e
		}
		a, b, c := makeEvent(1800000000, 1), makeEvent(1800000003, 2), makeEvent(1800000000, 500)
		if a.Snapshot.Location.Latitude != c.Snapshot.Location.Latitude || a.Snapshot.Location.Longitude != c.Snapshot.Location.Longitude {
			t.Fatal("movement depends on number of observations", kind)
		}
		x := (b.Snapshot.Location.Longitude - a.Snapshot.Location.Longitude) * 111320 * math.Cos(31.23*math.Pi/180)
		y := (b.Snapshot.Location.Latitude - a.Snapshot.Location.Latitude) * 111320
		distance := math.Hypot(x, y)
		if kind == "sensor" || kind == "facility" {
			if distance != 0 {
				t.Fatal("fixed entity moved", kind)
			}
		} else if distance < 15 {
			t.Fatal("three-second displacement is not visible", kind, distance)
		}
	}
}

func TestDemoRoutesStayInsideMapForFullCycle(t *testing.T) {
	for _, kind := range []string{"person", "drone", "vehicle", "robot"} {
		for tick := 0; tick < 300; tick++ {
			x, y := demoRoute(kind, float64(tick), .37)
			if x < 30 || x > 970 || y < 30 || y > 610 {
				t.Fatal("route leaves map", kind, tick, x, y)
			}
		}
	}
}
