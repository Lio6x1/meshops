package edge

import (
	"bytes"
	"context"
	"encoding/json"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"math/rand"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func courseCredentials(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PERSON_SOURCE", "DRONE_SOURCE", "VEHICLE_SOURCE", "ROBOT_SOURCE", "SENSOR_SOURCE", "FACILITY_SOURCE", "PERSON_EXECUTOR", "DRONE_EXECUTOR", "VEHICLE_EXECUTOR", "ROBOT_EXECUTOR", "OPERATOR", "ADMIN", "TASK", "DISPATCHER"} {
		t.Setenv("MESHOPS_"+name+"_TOKEN", strings.Repeat("test-only-", 4)+name)
	}
}

// A12/A24: execute the real CLI functions against disk and a real TCP gRPC peer.
func TestGatewayCLIHundredOfflineEventsRestartAndDrain(t *testing.T) {
	courseCredentials(t)
	path := filepath.Join(t.TempDir(), "person.db")
	args := []string{"--manifest", "../../configs/simulation.yaml", "--source", "personnel_sim", "--db", path, "--offline", "--count", "100", "--rate", "1000", "--duration", "10s"}
	var out, diagnostic bytes.Buffer
	if code := GatewayCLI(context.Background(), args, &out, &diagnostic); code != 0 {
		t.Fatal(code, out.String(), diagnostic.String())
	}
	var stats map[string]any
	if e := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &stats); e != nil {
		t.Fatal(e, out.String())
	}
	if stats["generated"] != float64(100) || stats["pending"] != float64(100) {
		t.Fatal(stats)
	}
	epoch := stats["epoch"]
	q, e := OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := q.Pending(100)
	if e != nil || len(p) != 100 || p[99].Event.EntityVersion != 100 {
		t.Fatal(len(p), e)
	}
	q.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := grpc.NewServer()
	receiver := new(uploadServer)
	ingestv1.RegisterIngestServiceServer(server, receiver)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	out.Reset()
	diagnostic.Reset()
	args = []string{"--manifest", "../../configs/simulation.yaml", "--source", "personnel_sim", "--db", path, "--drain-only", "--endpoint", listener.Addr().String(), "--timeout", "5s"}
	if code := GatewayCLI(context.Background(), args, &out, &diagnostic); code != 0 {
		t.Fatal(code, out.String(), diagnostic.String())
	}
	stats = nil
	if e = json.Unmarshal(bytes.TrimSpace(out.Bytes()), &stats); e != nil {
		t.Fatal(e, out.String())
	}
	if stats["epoch"] != epoch || stats["pending"] != float64(0) || stats["confirmedSequence"] != float64(100) {
		t.Fatal(stats)
	}
	q, e = OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	seq, e := q.Generate("demo_tenant:person-001", func(v int64) (*commonv1.EntityStateEvent, error) {
		if v != 101 {
			t.Fatalf("version reset: %d", v)
		}
		return &commonv1.EntityStateEvent{TenantId: "demo_tenant", EntityId: "person-001", EntityVersion: v}, nil
	})
	if e != nil || seq != 101 {
		t.Fatal(seq, e)
	}
}
func TestGatewayCLIProducesEverySourceAndPreservesDuplicate(t *testing.T) {
	courseCredentials(t)
	for _, pair := range [][2]string{{"personnel_sim", "person"}, {"drone_sim", "drone"}, {"vehicle_sim", "vehicle"}, {"robot_sim", "robot"}, {"sensor_sim", "sensor"}, {"facility_sim", "facility"}} {
		t.Run(pair[1], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queue.db")
			var out bytes.Buffer
			args := []string{"--manifest", "../../configs/simulation.yaml", "--source", pair[0], "--db", path, "--offline", "--count", "2", "--rate", "1000", "--duplicate-every", "2"}
			if code := GatewayCLI(context.Background(), args, &out, &out); code != 0 {
				t.Fatal(code, out.String())
			}
			q, e := OpenQueue(path)
			if e != nil {
				t.Fatal(e)
			}
			defer q.Close()
			p, e := q.Pending(100)
			if e != nil || len(p) != 3 {
				t.Fatal(e, len(p))
			}
			if p[0].Event.Snapshot.EntityType != pair[1] || p[1].Event.EntityVersion != 2 || !proto.Equal(p[1].Event, p[2].Event) {
				t.Fatal(p)
			}
			if e = q.BindSource("different-source"); e == nil {
				t.Fatal("source reassignment accepted")
			}
		})
	}
}
func TestSimulationSeedReproducesMotionWithoutAddingPower(t *testing.T) {
	battery := 65.
	original := &commonv1.EntityStateEvent{Snapshot: &commonv1.EntitySnapshot{EntityType: "robot", Location: &commonv1.Location{Latitude: 30, Longitude: 120}, Power: &commonv1.PowerState{BatteryPercent: &battery}}}
	a, b := proto.Clone(original).(*commonv1.EntityStateEvent), proto.Clone(original).(*commonv1.EntityStateEvent)
	simulateMotion(a, rand.New(rand.NewSource(7)))
	simulateMotion(b, rand.New(rand.NewSource(7)))
	if !proto.Equal(a, b) || proto.Equal(a, original) {
		t.Fatal("motion is not deterministic")
	}
	person := &commonv1.EntityStateEvent{Snapshot: &commonv1.EntitySnapshot{EntityType: "person"}}
	simulateMotion(person, rand.New(rand.NewSource(7)))
	if person.Snapshot.Power != nil || person.Snapshot.Location != nil {
		t.Fatal("invented absent component")
	}
}
func TestGatewayDrainTimeoutPreservesQueue(t *testing.T) {
	courseCredentials(t)
	path := filepath.Join(t.TempDir(), "queue.db")
	q, e := OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	generate(t, q)
	q.Close()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := GatewayCLI(ctx, []string{"--manifest", "../../configs/simulation.yaml", "--source", "personnel_sim", "--db", path, "--drain-only", "--endpoint", "127.0.0.1:1", "--timeout", "100ms"}, &out, ioDiscard{})
	if code != 1 {
		t.Fatal(code, out.String())
	}
	q, e = OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	s, _ := q.Stats()
	if s.Pending != 1 {
		t.Fatal(fmt.Sprint(s))
	}
}
