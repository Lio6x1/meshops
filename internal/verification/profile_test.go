package verification

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
)

func TestBenchmarkProfilesAndDriver(t *testing.T) {
	for _, profile := range []string{"person", "mixed"} {
		t.Run(profile, func(t *testing.T) {
			plan, err := benchmarkSources(profile, 10000, 10)
			if err != nil {
				t.Fatal(err)
			}
			env := &Environment{}
			want := map[string]int{"person": 10000}
			if profile == "mixed" {
				want = map[string]int{"person": 2000, "drone": 2000, "vehicle": 2000, "robot": 2000, "sensor": 1000, "facility": 1000}
			}
			counts := map[string]int{}
			for i, spec := range plan {
				if len(spec.IDs) != 1000 {
					t.Fatal("unbalanced source", i)
				}
				counts[spec.Adapter] += len(spec.IDs)
				if spec.Executable != (spec.Adapter != "sensor" && spec.Adapter != "facility") {
					t.Fatal("incorrect task capability", spec.Adapter)
				}
				mapping := map[string]string{}
				for _, id := range spec.IDs {
					mapping["raw_"+id] = id
				}
				env.Sources = append(env.Sources, platform.Source{TenantID: "test", ID: spec.Adapter, Adapter: spec.Adapter, Generation: 1, Fixture: filepath.Join("..", "..", "testdata", "sources", spec.Adapter+".json"), Entities: mapping})
			}
			for kind, n := range want {
				if counts[kind] != n {
					t.Fatalf("%s: %d, want %d", kind, counts[kind], n)
				}
			}
			driver, err := newLoadDriver(env)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for i := 0; i < 10000; i++ {
				job, err := driver.job(false)
				if err != nil {
					t.Fatal(err)
				}
				if seen[job.event.EntityId] || job.event.EntityVersion != 1 || job.source != i%10 {
					t.Fatal("driver repeated/skipped entity or source", i)
				}
				if job.event.Snapshot.EntityType != plan[job.source].Adapter {
					t.Fatal("wrong normalized type")
				}
				seen[job.event.EntityId] = true
			}
			job, err := driver.job(false)
			if err != nil || job.event.EntityVersion != 2 {
				t.Fatal("version was not per entity", err)
			}
		})
	}
}

func TestLoadDriverCachesEachSourceFixture(t *testing.T) {
	source := platform.Source{TenantID: "test", ID: "source", Adapter: "person", Generation: 1, Fixture: filepath.Join("..", "..", "testdata", "sources", "person.json"), Entities: map[string]string{"raw": "entity"}}
	raw, err := os.ReadFile(source.Fixture)
	if err != nil {
		t.Fatal(err)
	}
	source.Fixture = filepath.Join(t.TempDir(), "person.json")
	if err = os.WriteFile(source.Fixture, raw, 0600); err != nil {
		t.Fatal(err)
	}
	driver, err := newLoadDriver(&Environment{Sources: []platform.Source{source}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(source.Fixture); err != nil {
		t.Fatal(err)
	}
	for version := int64(1); version <= 2; version++ {
		job, err := driver.job(false)
		if err != nil || job.event.EntityVersion != version {
			t.Fatal("fixture was reread or version lost", err)
		}
	}
}

func BenchmarkLoadDriverFixture(b *testing.B) {
	root := os.Getenv("MESHOPS_BENCH_FIXTURE_ROOT")
	if root == "" {
		root = filepath.Join("..", "..", "testdata", "sources")
	}
	source := platform.Source{TenantID: "test", ID: "source", Adapter: "person", Generation: 1, Fixture: filepath.Join(root, "person.json"), Entities: map[string]string{"raw": "entity"}}
	b.Run("disk", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			now := time.Now().UTC()
			raw, err := state.GenerateRaw(source, "raw", int64(i+1), now)
			if err != nil {
				b.Fatal(err)
			}
			if _, err = state.Normalize(raw, source, now); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		driver, err := newLoadDriver(&Environment{Sources: []platform.Source{source}})
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err = driver.job(false); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestInvalidBenchmarkProfileAndDimensions(t *testing.T) {
	for _, tc := range []struct {
		profile           string
		entities, sources int
	}{{"unknown", 10000, 10}, {"mixed", 10, 1}, {"mixed", 0, 10}, {"person", 11, 10}} {
		if _, err := benchmarkSources(tc.profile, tc.entities, tc.sources); err == nil {
			t.Fatal("accepted invalid profile", tc)
		}
	}
}

func TestObservationSamplingCoversEverySource(t *testing.T) {
	counts := make([]int, 10)
	for i := 0; i < 3000; i++ {
		if observeScheduled(i, 10) {
			counts[i%10]++
		}
	}
	for i, n := range counts {
		if n != 6 {
			t.Fatalf("source %d got %d observations, want 6", i, n)
		}
	}
}
