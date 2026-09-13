package state

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"example.com/meshops-course/internal/platform"
)

func generatorSource(adapter string) platform.Source {
	root := os.Getenv("MESHOPS_BENCH_FIXTURE_ROOT")
	if root == "" {
		root = filepath.Join("..", "..", "testdata", "sources")
	}
	return platform.Source{TenantID: "test", ID: "source", Adapter: adapter, Generation: 1, Fixture: filepath.Join(root, adapter+".json"), StaleAfter: 30 * time.Second, Entities: map[string]string{"raw-a": "entity-a", "raw-b": "entity-b"}}
}

func TestRawGeneratorSixFormatsAndFreshIdentity(t *testing.T) {
	for _, adapter := range []string{"person", "drone", "vehicle", "robot", "sensor", "facility"} {
		t.Run(adapter, func(t *testing.T) {
			source := generatorSource(adapter)
			generator, err := NewRawGenerator(source)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for i, id := range []string{"raw-a", "raw-b", "raw-a"} {
				at := time.Date(2026, 9, 13, 1, 2, i, 0, time.UTC)
				raw, err := generator.Generate(id, int64(i+1), at)
				if err != nil {
					t.Fatal(err)
				}
				event, err := Normalize(raw, source, at)
				if err != nil {
					t.Fatal(err)
				}
				if event.EntityId != source.Entities[id] || event.EntityVersion != int64(i+1) || event.Snapshot.EntityType != adapter || !event.OccurredAt.AsTime().Equal(at) || seen[event.EventId] {
					t.Fatal("stale or corrupt generated event", event)
				}
				seen[event.EventId] = true
			}
			for _, input := range []struct {
				id      string
				version int64
			}{{"unknown", 1}, {"raw-a", 0}, {"raw-a", MaxVersion + 1}} {
				if _, err := generator.Generate(input.id, input.version, time.Now()); err == nil {
					t.Fatal("accepted invalid identity or version")
				}
			}
		})
	}
}

func TestRawGeneratorReadsOnceAndDoesNotMutateTemplate(t *testing.T) {
	source := generatorSource("person")
	raw, err := os.ReadFile(source.Fixture)
	if err != nil {
		t.Fatal(err)
	}
	source.Fixture = filepath.Join(t.TempDir(), "person.json")
	if err = os.WriteFile(source.Fixture, raw, 0600); err != nil {
		t.Fatal(err)
	}
	generator, err := NewRawGenerator(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(source.Fixture); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	var group sync.WaitGroup
	failures := make(chan error, 32)
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(version int64) {
			defer group.Done()
			out, err := generator.Generate("raw-a", version, at)
			if err == nil {
				event, x := Normalize(out, source, at)
				err = x
				if err == nil && event.EntityVersion != version {
					err = fmt.Errorf("shared map changed version")
				}
			}
			failures <- err
		}(int64(i + 1))
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRawGeneratorPreservesFormatRejection(t *testing.T) {
	source := generatorSource("person")
	raw, err := os.ReadFile(source.Fixture)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("{"), []byte(`{"unknown":true,`), 1)
	source.Fixture = filepath.Join(t.TempDir(), "invalid.json")
	if err = os.WriteFile(source.Fixture, raw, 0600); err != nil {
		t.Fatal(err)
	}
	generator, err := NewRawGenerator(source)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	out, err := generator.Generate("raw-a", 1, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Normalize(out, source, at); err == nil {
		t.Fatal("cached template bypassed strict format validation")
	}
	source.Adapter = "unknown"
	if _, err = NewRawGenerator(source); err == nil {
		t.Fatal("unknown adapter accepted")
	}
	if err = os.WriteFile(source.Fixture, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	source.Adapter = "person"
	if _, err = NewRawGenerator(source); err == nil {
		t.Fatal("malformed template accepted")
	}
}

func BenchmarkGenerateRawCached(b *testing.B) {
	source := generatorSource("person")
	generator, err := NewRawGenerator(source)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := generator.Generate("raw-a", int64(i+1), now); err != nil {
			b.Fatal(err)
		}
	}
}

// 保留逐条读盘的对照组；不包含 Kafka 或投影成本。
func BenchmarkGenerateRawDisk(b *testing.B) {
	source := generatorSource("person")
	now := time.Now().UTC()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateRaw(source, "raw-a", int64(i+1), now); err != nil {
			b.Fatal(err)
		}
	}
}
