package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
)

func TestScaleDimensionsIndependentAndBounded(t *testing.T) {
	o := DefaultBenchmarkOptions()
	if o.Entities != 10000 || !reflect.DeepEqual(o.Rates, []int{100, 500}) || o.Seconds != 30 {
		t.Fatal(o)
	}
	for _, n := range []int{10, 110, 10000, 100000, 1000000} {
		o.Entities = n
		if err := o.Validate(); err != nil {
			t.Fatal(n, err)
		}
		if !reflect.DeepEqual(o.Rates, []int{100, 500}) {
			t.Fatal("cardinality changed rate")
		}
	}
	for _, n := range []int{0, 11, 1000010} {
		o.Entities = n
		if o.Validate() == nil {
			t.Fatal("accepted", n)
		}
	}
	o = DefaultBenchmarkOptions()
	for _, rates := range [][]int{nil, {0}, {-1}, {100001}, {100, 100}} {
		o.Rates = rates
		if o.Validate() == nil {
			t.Fatal("accepted rates", rates)
		}
	}
	o = DefaultBenchmarkOptions()
	o.WarmupTimeout = 0
	if o.Validate() == nil {
		t.Fatal("unbounded warmup")
	}
}

func TestManifestUsesJSONWithoutBypassingRegistryValidation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHOPS_KAFKA_BROKERS", "unused:9092")
	t.Setenv("MESHOPS_REDIS_ADDR", "unused:6379")
	t.Setenv("MESHOPS_MYSQL_DSN", "invalid")
	// 在第一次数据库操作前主动失败，仅验证真实 manifest 生成与注册表解析。
	id := "fixture" + fmt.Sprint(time.Now().UnixNano())
	_, err = newEnvironmentAt(context.Background(), root, 110, 10, "mixed", id)
	if err == nil {
		t.Fatal("expected invalid DSN")
	}
	dir := filepath.Join(root, ".local", "verification", id)
	t.Cleanup(func() { os.RemoveAll(dir) })
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("manifest is not JSON")
	}
	registry, err := platform.LoadRegistry(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Bindings) != 110 || len(registry.Sources) != 10 {
		t.Fatal("missing registration")
	}
}

// 独立可调用的生成/加载基线；无数据库、Kafka、Redis访问。
func BenchmarkScaleManifest(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			root, err := filepath.Abs(filepath.Join("..", ".."))
			if err != nil {
				b.Fatal(err)
			}
			b.Setenv("MESHOPS_KAFKA_BROKERS", "unused:9092")
			b.Setenv("MESHOPS_REDIS_ADDR", "unused:6379")
			b.Setenv("MESHOPS_MYSQL_DSN", "invalid")
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				id := "profile" + platform.NewID()
				dir := filepath.Join(root, ".local", "verification", id)
				_, err = newEnvironmentAt(context.Background(), root, n, 10, "mixed", id)
				if err == nil {
					b.Fatal("expected pre-DB failure")
				}
				os.RemoveAll(dir)
			}
		})
	}
}

func TestWarmupChecksTailAndEveryIdentity(t *testing.T) {
	env := &Environment{Sources: []platform.Source{{Adapter: "person", Generation: 1, ID: "source"}}}
	d := &loadDriver{environment: env}
	for i := 0; i < 110; i++ {
		d.targets = append(d.targets, loadTarget{entityID: platform.NewID()})
	}
	for _, corrupt := range []int{-1, 109} {
		report := BenchmarkReport{WarmupSnapshotsByType: map[string]int{}}
		visited := 0
		err := verifyWarmup(context.Background(), d, &report, func(_ context.Context, ids []string) (*entityv1.BatchGetSnapshotsResponse, error) {
			if len(ids) > 100 {
				t.Fatal("RPC bound")
			}
			out := &entityv1.BatchGetSnapshotsResponse{}
			for _, id := range ids {
				snap := &entityv1.GetSnapshotResponse{Found: true, EntityId: id, SourceId: "source", SourceGeneration: 1, Version: 1, Snapshot: &commonv1.EntitySnapshot{EntityType: "person"}}
				if visited == corrupt {
					snap.EntityId = "wrong"
				}
				out.Snapshots = append(out.Snapshots, snap)
				visited++
			}
			return out, nil
		})
		if visited != 110 {
			t.Fatal("tail missed", visited)
		}
		if corrupt < 0 && (err != nil || report.WarmupSnapshots != 110) {
			t.Fatal(report, err)
		}
		if corrupt >= 0 && err == nil {
			t.Fatal("corrupt tail passed")
		}
	}
}

func TestPhaseCannotPassMissingWork(t *testing.T) {
	good := Phase{OfferedRate: 100, Requested: 500, Scheduled: 500, Accepted: 500, GenerationElapsed: 5, Elapsed: 5, ObservationCandidates: 10, Visible: Latency{Samples: 10}}
	if err := phaseFailure(good); err != nil {
		t.Fatal(err)
	}
	cases := []Phase{good, good, good, good, good, good, good, good, good, good}
	cases[0].GenerationElapsed = 6
	cases[1].QueueDrops = 1
	cases[2].ObservationMisses = 1
	cases[3].Scheduled = 499
	cases[4].Accepted = 499
	cases[5].Visible.Samples = 9
	cases[6].Errors = 1
	cases[7].Elapsed = 10
	cases[8].ProjectorLag = 1
	cases[9].HistoryLag = 1
	for i, p := range cases {
		if phaseFailure(p) == nil {
			t.Fatal("false success", i)
		}
	}
}

func TestConsumerDrainWaitsForBothGroups(t *testing.T) {
	calls := 0
	projector, history, err := drainConsumers(context.Background(), time.Second, func(context.Context) (int64, int64, error) {
		calls++
		switch calls {
		case 1:
			return 2, 4, nil
		case 2:
			return 0, 1, nil
		default:
			return 0, 0, nil
		}
	})
	if err != nil || projector != 0 || history != 0 || calls != 3 {
		t.Fatal("tail lag not drained", projector, history, calls, err)
	}
}

func TestConsumerDrainHonorsRemainingDeadlineAndPreservesLag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	began := time.Now()
	projector, history, err := drainConsumers(ctx, 30*time.Second, func(context.Context) (int64, int64, error) { return 0, 17, nil })
	if !errors.Is(err, context.DeadlineExceeded) || projector != 0 || history != 17 {
		t.Fatal(projector, history, err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("ignored remaining phase deadline")
	}
}

func TestConsumerDrainCapsProbeDeadlineAndRejectsProbeFailure(t *testing.T) {
	cause := errors.New("lag probe unavailable")
	_, _, err := drainConsumers(context.Background(), time.Hour, func(ctx context.Context) (int64, int64, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("unbounded lag probe")
		}
		return 7, 11, cause
	})
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
}

func TestInitializationFailureRetainsRequestedScale(t *testing.T) {
	t.Setenv("MESHOPS_KAFKA_BROKERS", "")
	root := t.TempDir()
	o := DefaultBenchmarkOptions()
	o.Entities = 1000000
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	report, err := BenchmarkWithOptions(ctx, root, o)
	if err == nil || report.Entities != 1000000 || report.Failure == "" || report.Stage != "initialize" {
		t.Fatal(report, err)
	}
	raw, e := os.ReadFile(filepath.Join(report.EvidenceDirectory, "benchmark.json"))
	if e != nil {
		t.Fatal(e)
	}
	var saved BenchmarkReport
	if json.Unmarshal(raw, &saved) != nil || saved.Failure == "" || saved.Entities != 1000000 {
		t.Fatal(string(raw))
	}
}
