package edge

import (
	commonv1 "example.com/meshops-course/gen/common/v1"
	"math"
	"math/rand"
)

// simulateMotion 为归一化测试数据加入有界、可复现的运动与电量变化，
// 不会凭空补出缺失的组件或能力。
// 观测时间仍使用真实时钟，事件标识仍按每次观测新生成。
func simulateMotion(event *commonv1.EntityStateEvent, rng *rand.Rand) {
	if event == nil || event.Snapshot == nil {
		return
	}
	s := event.Snapshot
	if s.EntityType == "sensor" || s.EntityType == "facility" {
		return
	}
	if s.Location != nil {
		s.Location.Latitude = math.Max(-90, math.Min(90, s.Location.Latitude+(rng.Float64()-.5)*.00002))
		s.Location.Longitude = math.Max(-180, math.Min(180, s.Location.Longitude+(rng.Float64()-.5)*.00002))
	}
	if s.Power != nil && s.Power.BatteryPercent != nil {
		battery := math.Max(0, math.Min(100, *s.Power.BatteryPercent+(rng.Float64()-.5)*2))
		s.Power.BatteryPercent = &battery
	}
}
