package state

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"example.com/meshops-course/internal/platform"
)

// RawGenerator 保存只读来源样例。注册表的实体归属映射在运行期同样只读；
// Generate 为每条事件创建独立映射，支持并发调用且不会修改缓存样例。
// 生成结果仍必须经过 Normalize 的六类格式与业务校验。
type RawGenerator struct {
	source   platform.Source
	template map[string]json.RawMessage
	fields   [4]string
}

func NewRawGenerator(source platform.Source) (*RawGenerator, error) {
	fields := map[string][4]string{"person": {"employee_id", "version", "observed_at", "observation_id"}, "drone": {"aircraft_id", "revision", "timestamp_ms", "sample_id"}, "vehicle": {"vehicle_id", "sequence", "recorded_at", "event_id"}, "robot": {"robot_id", "revision", "timestamp", "event_id"}, "sensor": {"sensor_id", "version", "measured_at", "event_id"}, "facility": {"facility_id", "revision", "observed_at", "event_id"}}
	f, ok := fields[source.Adapter]
	if !ok {
		return nil, fmt.Errorf("unknown adapter")
	}
	raw, err := os.ReadFile(source.Fixture)
	if err != nil {
		return nil, err
	}
	var template map[string]json.RawMessage
	if err = json.Unmarshal(raw, &template); err != nil {
		return nil, err
	}
	if template == nil {
		return nil, fmt.Errorf("source fixture must be a JSON object")
	}
	return &RawGenerator{source: source, template: template, fields: f}, nil
}

func (g *RawGenerator) Generate(rawID string, version int64, now time.Time) ([]byte, error) {
	if _, ok := g.source.Entities[rawID]; !ok || version < 1 || version > MaxVersion {
		return nil, fmt.Errorf("unregistered ID or invalid version")
	}
	m := make(map[string]json.RawMessage, len(g.template))
	for key, value := range g.template {
		m[key] = value
	}
	var timestamp any = now.UTC().Format(time.RFC3339Nano)
	if g.source.Adapter == "drone" {
		timestamp = now.UnixMilli()
	}
	values := [4]any{rawID, version, timestamp, newID()}
	for i, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		m[g.fields[i]] = encoded
	}
	return json.Marshal(m)
}
