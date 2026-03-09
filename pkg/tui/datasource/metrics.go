package datasource

import (
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// RuntimeMetrics collects Go runtime metrics
type RuntimeMetrics struct{}

// NewRuntimeMetrics creates a new runtime metrics collector
func NewRuntimeMetrics() *RuntimeMetrics {
	return &RuntimeMetrics{}
}

// Collect gathers current runtime metrics
func (m *RuntimeMetrics) Collect() MetricsSnapshot {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	snap := MetricsSnapshot{
		MemTotal:      stats.Sys,
		MemAlloc:      stats.Alloc,
		HeapAlloc:     stats.HeapAlloc,
		NumGoroutines: runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		NumGC:         stats.NumGC,
	}

	// System-level metrics via gopsutil (non-fatal on error)
	if cpuPcts, err := cpu.Percent(0, false); err == nil && len(cpuPcts) > 0 {
		snap.CPUPercent = cpuPcts[0]
	}
	if vmem, err := mem.VirtualMemory(); err == nil {
		snap.MemUsedPercent = vmem.UsedPercent
	}
	if avg, err := load.Avg(); err == nil {
		snap.LoadAvg1 = avg.Load1
	}

	return snap
}
