package verification

import (
	"errors"
	"fmt"
	"sort"

	"example.com/meshops-course/internal/platform"
)

// A profile changes the input mix, not the tested services or offered rate.
// Each source still owns one adapter and an equal number of authoritative IDs.
type benchmarkSource struct {
	Adapter    string
	IDs        []string
	Executable bool
}

func ValidateBenchmarkProfile(profile string) error {
	if profile != "person" && profile != "mixed" {
		return errors.New("benchmark profile must be person or mixed")
	}
	return nil
}

func benchmarkSources(profile string, entities, sources int) ([]benchmarkSource, error) {
	if err := ValidateBenchmarkProfile(profile); err != nil {
		return nil, err
	}
	if entities < 1 || sources < 1 || entities%sources != 0 {
		return nil, errors.New("entities must be a positive multiple of sources")
	}
	adapters := []string{"person", "person", "drone", "drone", "vehicle", "vehicle", "robot", "robot", "sensor", "facility"}
	if profile == "mixed" && sources != len(adapters) {
		return nil, errors.New("mixed profile requires 10 sources")
	}
	plan := make([]benchmarkSource, sources)
	for s := range plan {
		adapter := "person"
		if profile == "mixed" {
			adapter = adapters[s]
		}
		plan[s] = benchmarkSource{Adapter: adapter, Executable: adapter != "sensor" && adapter != "facility"}
		for i := s * entities / sources; i < (s+1)*entities/sources; i++ {
			plan[s].IDs = append(plan[s].IDs, fmt.Sprintf("%s-%05d", adapter, i))
		}
	}
	return plan, nil
}

type loadTarget struct {
	source          int
	rawID, entityID string
}

// Materialize the registry once. Hot-path generation must not sort or scan a
// thousand-entry map for each event. Raw IDs need not equal canonical IDs.
func newLoadDriver(env *Environment) (*loadDriver, error) {
	if len(env.Sources) == 0 {
		return nil, errors.New("load driver requires sources")
	}
	lists := make([][]string, len(env.Sources))
	max := 0
	for i, source := range env.Sources {
		lists[i] = sourceRawIDs(source)
		if len(lists[i]) == 0 {
			return nil, errors.New("load source has no entities")
		}
		if len(lists[i]) > max {
			max = len(lists[i])
		}
	}
	d := &loadDriver{environment: env}
	seen := map[string]bool{}
	for row := 0; row < max; row++ {
		for s, ids := range lists {
			if row >= len(ids) {
				continue
			}
			id := env.Sources[s].Entities[ids[row]]
			if id == "" || seen[id] {
				return nil, errors.New("load entity missing or assigned twice")
			}
			seen[id] = true
			d.targets = append(d.targets, loadTarget{s, ids[row], id})
		}
	}
	d.versions = make([]int64, len(d.targets))
	return d, nil
}

func sourceRawIDs(source platform.Source) []string {
	ids := make([]string, 0, len(source.Entities))
	for id := range source.Entities {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Taking indices 0,50,100 with ten round-robin sources only samples source 0.
// Rotate the one-in-50 observation slot so all ten adapters/sources are seen.
func observeScheduled(index, sources int) bool { return index%50 == (index/50)%sources }
