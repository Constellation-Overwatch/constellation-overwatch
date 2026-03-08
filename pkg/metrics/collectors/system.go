package collectors

import (
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// SystemMetrics holds a snapshot of system resource usage.
type SystemMetrics struct {
	CPUPercent       float64
	MemUsed          uint64
	MemTotal         uint64
	MemPercent       float64
	LoadAvg1         float64
	LoadAvg5         float64
	LoadAvg15        float64
	NetBytesSentRate float64
	NetBytesRecvRate float64
	DiskPercent      float64
}

// SystemCollector gathers system-level metrics via gopsutil.
type SystemCollector struct {
	prevNetBytesSent uint64
	prevNetBytesRecv uint64
	prevTime         time.Time
	hasPrev          bool
}

// NewSystemCollector creates a new SystemCollector.
func NewSystemCollector() *SystemCollector {
	return &SystemCollector{}
}

// Collect gathers current system metrics.
func (c *SystemCollector) Collect() (SystemMetrics, error) {
	var m SystemMetrics

	// CPU usage (non-blocking, overall)
	cpuPcts, err := cpu.Percent(0, false)
	if err != nil {
		return m, fmt.Errorf("collecting cpu percent: %w", err)
	}
	if len(cpuPcts) > 0 {
		m.CPUPercent = cpuPcts[0]
	}

	// Memory
	vm, err := mem.VirtualMemory()
	if err != nil {
		return m, fmt.Errorf("collecting memory stats: %w", err)
	}
	m.MemUsed = vm.Used
	m.MemTotal = vm.Total
	m.MemPercent = vm.UsedPercent

	// Load averages
	avg, err := load.Avg()
	if err != nil {
		return m, fmt.Errorf("collecting load averages: %w", err)
	}
	m.LoadAvg1 = avg.Load1
	m.LoadAvg5 = avg.Load5
	m.LoadAvg15 = avg.Load15

	// Network I/O rates (delta-based)
	counters, err := net.IOCounters(false)
	if err != nil {
		return m, fmt.Errorf("collecting net counters: %w", err)
	}
	now := time.Now()
	if len(counters) > 0 {
		sent := counters[0].BytesSent
		recv := counters[0].BytesRecv

		if c.hasPrev {
			elapsed := now.Sub(c.prevTime).Seconds()
			if elapsed > 0 {
				m.NetBytesSentRate = float64(sent-c.prevNetBytesSent) / elapsed
				m.NetBytesRecvRate = float64(recv-c.prevNetBytesRecv) / elapsed
			}
		}

		c.prevNetBytesSent = sent
		c.prevNetBytesRecv = recv
		c.prevTime = now
		c.hasPrev = true
	}

	// Disk usage (root partition)
	du, err := disk.Usage("/")
	if err != nil {
		return m, fmt.Errorf("collecting disk usage: %w", err)
	}
	m.DiskPercent = du.UsedPercent

	return m, nil
}
