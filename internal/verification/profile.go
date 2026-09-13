package verification

import (
	"errors"
	"fmt"
	"sort"

	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
)

// 配置方案仅改变输入构成，被测服务和输入速率保持不变。
// 每个来源仍对应一个适配器，并拥有相同数量的权威 ID。
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

// 一次性构建注册表。热路径生成事件时，不能为每个事件
// 排序或扫描上千条目的映射。原始 ID 无需与规范 ID 相同。
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
	total := 0
	for _, ids := range lists {
		total += len(ids)
	}
	d := &loadDriver{environment: env, targets: make([]loadTarget, 0, total), generators: make([]*state.RawGenerator, len(env.Sources))}
	for i, source := range env.Sources {
		generator, err := state.NewRawGenerator(source)
		if err != nil {
			return nil, err
		}
		d.generators[i] = generator
	}
	seen := make(map[string]bool, total)
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

// 十个来源轮询输入时，采样索引 0、50、100 只能采到来源 0。
// 轮换每 50 条中的观测位置，确保十个适配器及来源均被观测。
func observeScheduled(index, sources int) bool { return index%50 == (index/50)%sources }
