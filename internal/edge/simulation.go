package edge

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/simulation"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"hash/fnv"
	"io"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// simulateMotion adds bounded, reproducible movement and battery variation to
// the normalized fixture. It never invents missing components or capabilities.
// Observation clocks and event identities intentionally remain real/fresh.
func simulateMotion(event *commonv1.EntityStateEvent, rng *rand.Rand) {
	if event == nil || event.Snapshot == nil {
		return
	}
	s := event.Snapshot
	if s.Location != nil {
		// Separate fixture positions without changing their source coordinate system.
		// Static equipment keeps a stable anchor; moving types use event time.
		h := fnv.New32a()
		_, _ = h.Write([]byte(event.SourceId + ":" + event.EntityId))
		bearing := float64(h.Sum32()%360) * math.Pi / 180
		lat, lon := .0015*math.Sin(bearing), .0015*math.Cos(bearing)
		// Six demonstration sites form two rows, leaving enough room for map
		// markers and labels. Hash-only anchors can collide even for these six IDs.
		// Unknown types retain a deterministic fallback around their own fixture.
		switch s.EntityType {
		case "person":
			lat, lon = .0015, -.003
		case "drone":
			lat, lon = .0015, 0
		case "vehicle":
			lat, lon = .0015, .003
		case "robot":
			lat, lon = -.0015, -.003
		case "sensor":
			lat, lon = -.0015, 0
		case "facility":
			lat, lon = -.0015, .003
		}
		// Each type has five separately registered fixture entities. Keep -001 at
		// its original site and place -002..005 around it in real coordinates.
		// This offset is independent of version, so fixed facilities never move.
		if suffix := strings.LastIndexByte(event.EntityId, '-'); suffix >= 0 {
			if n, err := strconv.Atoi(event.EntityId[suffix+1:]); err == nil && n >= 2 && n <= simulation.MaxEntitiesPerSource {
				angle := float64(n-2)*math.Pi/2 + math.Pi/4
				lat += .0008 * math.Sin(angle)
				lon += .0008 * math.Cos(angle)
			}
		}
		if s.EntityType != "sensor" && s.EntityType != "facility" {
			seconds := float64(event.EntityVersion%120000) / 2 // deterministic fallback for timeless unit fixtures
			if event.OccurredAt != nil && event.OccurredAt.CheckValid() == nil {
				seconds = float64(event.OccurredAt.AsTime().UnixMilli()) / 1000
			}
			offset := float64(h.Sum32()%1000) / 1000
			if suffix := strings.LastIndexByte(event.EntityId, '-'); suffix >= 0 {
				if n, err := strconv.Atoi(event.EntityId[suffix+1:]); err == nil && n >= 1 && n <= simulation.MaxEntitiesPerSource {
					offset = float64(n-1) / simulation.MaxEntitiesPerSource
				}
			}
			x, y := demoRoute(s.EntityType, seconds, offset)
			lat, lon = (320-y)/111320, (x-500)/(111320*math.Cos(s.Location.Latitude*math.Pi/180))
			if s.Velocity != nil {
				nx, ny := demoRoute(s.EntityType, seconds+.01, offset)
				s.Velocity.Speed = math.Hypot(nx-x, ny-y) / .01
				heading := math.Mod(math.Atan2(nx-x, y-ny)*180/math.Pi+360, 360)
				s.Velocity.Heading = &heading
			}
		}
		s.Location.Latitude = math.Max(-90, math.Min(90, s.Location.Latitude+lat))
		s.Location.Longitude = math.Max(-180, math.Min(180, s.Location.Longitude+lon))
	}
	if s.EntityType == "sensor" || s.EntityType == "facility" {
		return
	}
	if s.Power != nil && s.Power.BatteryPercent != nil {
		battery := math.Max(0, math.Min(100, *s.Power.BatteryPercent+(rng.Float64()-.5)*2))
		s.Power.BatteryPercent = &battery
	}
}

type simulationControlStore interface {
	Desired(context.Context, string, string) (simulation.Mode, error)
	DesiredCount(context.Context, string, string) (int, error)
	Observe(context.Context, string, string, simulation.Observation) error
}

// runControlledSimulation owns the sole upload worker. Transition acknowledgements
// are published only after the old stream is cancelled and joined. Paused stops
// both generation and transport; offline keeps the durable queue growing.
func runControlledSimulation(ctx context.Context, store simulationControlStore, tenant, source string, capacity int, pollInterval, generateInterval time.Duration, generate func(int) error, upload func(context.Context) error, observation func() simulation.Observation, diagnostic io.Writer) (runErr error) {
	mode := simulation.Paused
	activeCount := 0
	var stopUpload context.CancelFunc
	var uploadDone chan error
	shutdownResult := func(err error) error {
		if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded) {
			return nil
		}
		return err
	}
	stop := func() error {
		if stopUpload == nil {
			return nil
		}
		stopUpload()
		err := joinUpload(uploadDone)
		stopUpload = nil
		uploadDone = nil
		return shutdownResult(err)
	}
	defer func() {
		if err := stop(); runErr == nil {
			runErr = err
		}
	}()
	refresh := func() error {
		controlCtx, cancel := context.WithTimeout(ctx, time.Second)
		desired, err := store.Desired(controlCtx, tenant, source)
		count := 0
		if err == nil {
			count, err = store.DesiredCount(controlCtx, tenant, source)
		}
		cancel()
		if err != nil {
			fmt.Fprintln(diagnostic, "simulation control unavailable; pausing source:", err)
			desired = simulation.Paused
			count = 0
		}
		if !desired.Valid() {
			return fmt.Errorf("invalid desired simulation mode %q", desired)
		}
		if count < 0 || count > simulation.MaxEntitiesPerSource {
			return fmt.Errorf("invalid desired simulation count %d", count)
		}
		// A custom opt-in manifest may contain fewer than five IDs. Acknowledge
		// only the subset actually selected; never pretend missing IDs exist.
		activeCount = min(count, capacity)
		if desired != mode {
			if err := stop(); err != nil {
				return err
			}
			mode = desired
			if mode == simulation.Running {
				var uploadCtx context.Context
				uploadCtx, stopUpload = context.WithCancel(ctx)
				uploadDone = make(chan error, 1)
				go func(done chan<- error) { done <- upload(uploadCtx) }(uploadDone)
			}
		}
		o := observation()
		o.Applied = mode
		o.ActiveCount = activeCount
		controlCtx, cancel = context.WithTimeout(ctx, time.Second)
		err = store.Observe(controlCtx, tenant, source, o)
		cancel()
		if err != nil {
			fmt.Fprintln(diagnostic, "simulation heartbeat unavailable; pausing source:", err)
			if stopErr := stop(); stopErr != nil {
				return stopErr
			}
			mode = simulation.Paused
		}
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	controlTick := time.NewTicker(pollInterval)
	defer controlTick.Stop()
	generateTick := time.NewTicker(generateInterval)
	defer generateTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-uploadDone:
			stopUpload()
			stopUpload = nil
			uploadDone = nil
			// Cancellation and the worker result may both be ready. The select
			// ordering must not turn a normal count/duration stop into a failure.
			return shutdownResult(err)
		case <-controlTick.C:
			if err := refresh(); err != nil {
				return err
			}
		case <-generateTick.C:
			if mode != simulation.Paused && activeCount > 0 {
				if err := generate(activeCount); err != nil {
					return err
				}
			}
		}
	}
}
