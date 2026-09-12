package edge

import "math"

// demoRoute 返回 1000 x 640 示意图中的米制坐标。为便于简短演示，
// 运动周期被刻意加快，不作为导航或物理模型。
// 基于时间而非版本计算位置，使速度不受活动实体数量影响。
func demoRoute(kind string, seconds, offset float64) (x, y float64) {
	period := 60.0
	switch kind {
	case "person":
		period = 50
	case "drone":
		period = 35
	case "vehicle":
		period = 65
	}
	phase := math.Mod(seconds/period+offset, 1)
	if phase < 0 {
		phase++
	}
	switch kind {
	case "drone":
		angle := phase * 2 * math.Pi
		return 620 + 210*math.Cos(angle), 160 + 110*math.Sin(angle)
	case "person":
		return rectangleRoute(phase, 45, 40, 330, 270)
	case "vehicle":
		// 主要横纵道路，以及南侧和东侧的外围道路。
		return rectangleRoute(phase, 450, 340, 940, 590)
	default:
		return rectangleRoute(phase, 45, 405, 325, 590)
	}
}

// 沿每条边匀速移动；用周长距离计算，避免矩形长边与短边
// 看起来速度不同。
func rectangleRoute(phase, left, top, right, bottom float64) (float64, float64) {
	width, height := right-left, bottom-top
	distance := phase * 2 * (width + height)
	switch {
	case distance < width:
		return left + distance, top
	case distance < width+height:
		return right, top + distance - width
	case distance < 2*width+height:
		return right - (distance - width - height), bottom
	default:
		return left, bottom - (distance - 2*width - height)
	}
}
