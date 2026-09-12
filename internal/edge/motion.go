package edge

import "math"

// demoRoute returns metres in the 1000 x 640 schematic. Periods are deliberately
// accelerated for a short demonstration, not a navigation or physics model.
// Time, rather than version, keeps speed independent of the active entity count.
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
		// Main horizontal and vertical roads plus the south/east perimeter road.
		return rectangleRoute(phase, 450, 340, 940, 590)
	default:
		return rectangleRoute(phase, 45, 405, 325, 590)
	}
}

// Walk each edge at constant speed; using perimeter distance avoids different
// apparent speeds on the long and short sides of a rectangle.
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
