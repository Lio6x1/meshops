package edge

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/simulation"
)

type simulationTestStore struct {
	mu           sync.Mutex
	mode         simulation.Mode
	failure      bool
	observations chan simulation.Observation
}

func (s *simulationTestStore) Desired(context.Context, string, string) (simulation.Mode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure {
		return "", errors.New("redis unavailable")
	}
	return s.mode, nil
}
func (s *simulationTestStore) Observe(_ context.Context, _, _ string, o simulation.Observation) error {
	s.observations <- o
	return nil
}
func (s *simulationTestStore) set(mode simulation.Mode, failure bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	s.failure = failure
}

func TestSimulationControlsJoinUploaderAndFailClosed(t *testing.T) {
	store := &simulationTestStore{mode: simulation.Running, observations: make(chan simulation.Observation, 100)}
	var generated, active, starts atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runControlledSimulation(ctx, store, "tenant", "source", 5*time.Millisecond, 2*time.Millisecond, func() error { generated.Add(1); return nil }, func(ctx context.Context) error {
			active.Add(1)
			starts.Add(1)
			defer active.Add(-1)
			<-ctx.Done()
			time.Sleep(8 * time.Millisecond)
			return ctx.Err()
		}, func() simulation.Observation { return simulation.Observation{} }, io.Discard)
	}()
	wait := func(mode simulation.Mode) {
		t.Helper()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case o := <-store.observations:
				if o.Applied == mode {
					return
				}
			case <-timer.C:
				t.Fatalf("no applied mode %s", mode)
			}
		}
	}
	wait(simulation.Running)
	store.set(simulation.Offline, false)
	wait(simulation.Offline)
	if active.Load() != 0 {
		t.Fatal("reported offline before upload joined")
	}
	before := generated.Load()
	time.Sleep(15 * time.Millisecond)
	if generated.Load() <= before {
		t.Fatal("offline must keep generating backlog")
	}
	store.set(simulation.Paused, false)
	wait(simulation.Paused)
	before = generated.Load()
	time.Sleep(15 * time.Millisecond)
	if generated.Load() != before {
		t.Fatal("paused generated data")
	}
	store.set(simulation.Running, false)
	wait(simulation.Running)
	store.set(simulation.Running, true)
	wait(simulation.Paused)
	if active.Load() != 0 {
		t.Fatal("Redis failure left upload active")
	}
	store.set(simulation.Running, false)
	wait(simulation.Running)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 || starts.Load() < 2 {
		t.Fatal("uploader leak or resume failed")
	}
}

func TestSimulationTrajectoryDistinctBoundedAndStable(t *testing.T) {
	makeEvent := func(kind string, version int64) *commonv1.EntityStateEvent {
		return &commonv1.EntityStateEvent{SourceId: kind + "_sim", EntityId: kind + "-001", EntityVersion: version, Snapshot: &commonv1.EntitySnapshot{EntityType: kind, Location: &commonv1.Location{Latitude: 31, Longitude: 121}}}
	}
	positions := map[[2]float64]bool{}
	for _, kind := range []string{"person", "drone", "vehicle", "robot", "sensor", "facility"} {
		a, b := makeEvent(kind, 1), makeEvent(kind, 2)
		simulateMotion(a, rand.New(rand.NewSource(1)))
		simulateMotion(b, rand.New(rand.NewSource(1)))
		p := [2]float64{a.Snapshot.Location.Latitude, a.Snapshot.Location.Longitude}
		if positions[p] {
			t.Fatal("entities overlap", kind)
		}
		positions[p] = true
		same := a.Snapshot.Location.Latitude == b.Snapshot.Location.Latitude && a.Snapshot.Location.Longitude == b.Snapshot.Location.Longitude
		if same != (kind == "sensor" || kind == "facility") {
			t.Fatal("incorrect movement", kind)
		}
		if p[0] < 30.99 || p[0] > 31.01 || p[1] < 120.99 || p[1] > 121.01 {
			t.Fatal("unbounded trajectory")
		}
	}
}

func TestSimulationSixMapPinsRemainSeparated(t *testing.T) {
	for _, version := range []int64{1, 45, 90, 135, 180, 225, 270, 315, 360, 9007199254740991} {
		var positions [][2]float64
		for _, kind := range []string{"person", "drone", "vehicle", "robot", "sensor", "facility"} {
			e := &commonv1.EntityStateEvent{SourceId: kind + "_sim", EntityId: kind + "-001", EntityVersion: version, Snapshot: &commonv1.EntitySnapshot{EntityType: kind, Location: &commonv1.Location{Latitude: 31.23, Longitude: 121.47}}}
			simulateMotion(e, rand.New(rand.NewSource(1)))
			lat, lon := e.Snapshot.Location.Latitude, e.Snapshot.Location.Longitude
			if math.Abs(lat-31.23) > .0026 || math.Abs(lon-121.47) > .004 {
				t.Fatalf("%s leaves bounded map at version %d", kind, version)
			}
			for _, other := range positions {
				x := (lon - other[1]) * 111320 * math.Cos(31.23*math.Pi/180)
				y := (lat - other[0]) * 111320
				if math.Hypot(x, y) < 200 {
					t.Fatalf("%s pin overlaps another demo site: %.1fm at version %d", kind, math.Hypot(x, y), version)
				}
			}
			positions = append(positions, [2]float64{lat, lon})
		}
	}
}

func TestSimulationCancellationIsSuccessfulEvenIfUploaderFinishesFirst(t *testing.T) {
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		store := &simulationTestStore{mode: simulation.Running, observations: make(chan simulation.Observation, 100)}
		err := runControlledSimulation(ctx, store, "tenant", "source", time.Second, time.Second, func() error { return nil }, func(ctx context.Context) error { cancel(); return ctx.Err() }, func() simulation.Observation { time.Sleep(time.Millisecond); return simulation.Observation{} }, io.Discard)
		cancel()
		if err != nil {
			t.Fatalf("normal cancelled uploader reported failure: %v", err)
		}
	}
}

func TestSimulationDurationJoinsUploaderWithoutFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	store := &simulationTestStore{mode: simulation.Running, observations: make(chan simulation.Observation, 100)}
	err := runControlledSimulation(ctx, store, "tenant", "source", time.Second, time.Second, func() error { return nil }, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func() simulation.Observation { return simulation.Observation{} }, io.Discard)
	if err != nil {
		t.Fatalf("normal duration expiry reported failure: %v", err)
	}
}
