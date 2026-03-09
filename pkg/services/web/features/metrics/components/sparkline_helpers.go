package components

import (
	"fmt"
	"math"
	"strings"
)

// BuildLinePath converts points (raw values) to an SVG polyline points string.
// Points are scaled to fit within the given width and height.
func BuildLinePath(points []float64, width, height float64) string {
	if len(points) == 0 {
		return ""
	}

	minVal, maxVal := minMax(points)
	span := maxVal - minVal

	n := len(points)
	xStep := width / math.Max(float64(n-1), 1)

	var sb strings.Builder
	for i, p := range points {
		x := float64(i) * xStep
		var y float64
		if span == 0 {
			y = height / 2
		} else {
			normalized := (p - minVal) / span
			y = (1.0 - normalized) * height
		}
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%.1f,%.1f", x, y)
	}
	return sb.String()
}

// BuildAreaPath converts points to a closed SVG path for area fill.
func BuildAreaPath(points []float64, width, height float64) string {
	if len(points) == 0 {
		return ""
	}

	minVal, maxVal := minMax(points)
	span := maxVal - minVal

	n := len(points)
	xStep := width / math.Max(float64(n-1), 1)

	var sb strings.Builder
	for i, p := range points {
		x := float64(i) * xStep
		var y float64
		if span == 0 {
			y = height / 2
		} else {
			normalized := (p - minVal) / span
			y = (1.0 - normalized) * height
		}
		if i == 0 {
			fmt.Fprintf(&sb, "M%.1f,%.1f", x, y)
		} else {
			fmt.Fprintf(&sb, " L%.1f,%.1f", x, y)
		}
	}
	fmt.Fprintf(&sb, " L%.1f,%.1f L0,%.1f Z", width, height, height)
	return sb.String()
}

// FormatValue formats a float64 with a unit string for display.
func FormatValue(v float64, unit string) string {
	switch unit {
	case "%":
		return fmt.Sprintf("%.1f%%", v)
	case "MB":
		if v > 1048576 {
			return fmt.Sprintf("%.1f MB", v/(1024*1024))
		}
		return fmt.Sprintf("%.1f MB", v)
	case "msg/s", "/s":
		if v >= 1000 {
			return fmt.Sprintf("%.1fK %s", v/1000, unit)
		}
		return fmt.Sprintf("%.1f %s", v, unit)
	case "ms":
		return fmt.Sprintf("%.2f ms", v)
	default:
		if v == math.Trunc(v) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%.1f", v)
	}
}

// formatUint64 formats a uint64 with K/M suffix for large values.
func formatUint64(v uint64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// formatBytes formats bytes as human-readable "1.2 KB", "45.3 MB", etc.
func formatBytes(v uint64) string {
	switch {
	case v >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(v)/(1<<30))
	case v >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(v)/(1<<20))
	case v >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(v)/(1<<10))
	default:
		return fmt.Sprintf("%d B", v)
	}
}

// formatInt64 formats an int64 with K/M suffix for large values.
func formatInt64(v int64) string {
	abs := v
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.1fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func minMax(points []float64) (float64, float64) {
	if len(points) == 0 {
		return 0, 0
	}
	minVal := points[0]
	maxVal := points[0]
	for _, p := range points[1:] {
		if p < minVal {
			minVal = p
		}
		if p > maxVal {
			maxVal = p
		}
	}
	return minVal, maxVal
}
