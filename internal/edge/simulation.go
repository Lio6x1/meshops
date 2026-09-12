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
		// The version survives restarts in bbolt, so motion continues deterministically.
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
		if s.EntityType != "sensor" && s.EntityType != "facility" {
			phase := float64(event.EntityVersion%360)*math.Pi/180 + bearing
			lat += .00035 * math.Sin(phase)
			lon += .0004 * math.Cos(phase)
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
	Observe(context.Context, string, string, simulation.Observation) error
}

// runControlledSimulation owns the sole upload worker. Transition acknowledgements
// are published only after the old stream is cancelled and joined. Paused stops
// both generation and transport; offline keeps the durable queue growing.
func runControlledSimulation(ctx context.Context, store simulationControlStore, tenant, source string, pollInterval, generateInterval time.Duration, generate func() error, upload func(context.Context) error, observation func() simulation.Observation, diagnostic io.Writer) (runErr error) {
	mode := simulation.Paused
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
		cancel()
		if err != nil {
			fmt.Fprintln(diagnostic, "simulation control unavailable; pausing source:", err)
			desired = simulation.Paused
		}
		if !desired.Valid() {
			return fmt.Errorf("invalid desired simulation mode %q", desired)
		}
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
			if mode != simulation.Paused {
				if err := generate(); err != nil {
					return err
				}
			}
		}
	}
}
