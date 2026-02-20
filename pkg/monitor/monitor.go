package monitor

import (
	"log"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// NodeMonitor continuously samples CPU and memory metrics for the local node.
type NodeMonitor struct {
	mu         sync.RWMutex
	cpuPercent float64
	memPercent float64
	memTotalMB uint64
	memUsedMB  uint64
	startTime  time.Time
}

// NewNodeMonitor creates a monitor and begins background sampling at the given interval.
func NewNodeMonitor(sampleInterval time.Duration) *NodeMonitor {
	m := &NodeMonitor{
		startTime: time.Now(),
	}
	go m.run(sampleInterval)
	return m
}

func (m *NodeMonitor) run(interval time.Duration) {
	// take an initial sample immediately
	m.sample()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		m.sample()
	}
}

func (m *NodeMonitor) sample() {
	// CPU – average over 1 second across all cores
	cpuPcts, err := cpu.Percent(time.Second, false)
	if err != nil {
		log.Printf("[monitor] cpu sample error: %v", err)
	}

	var cpuPct float64
	if len(cpuPcts) > 0 {
		cpuPct = cpuPcts[0]
	}

	// Memory
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("[monitor] memory sample error: %v", err)
	}

	m.mu.Lock()
	m.cpuPercent = cpuPct
	if vmStat != nil {
		m.memPercent = vmStat.UsedPercent
		m.memTotalMB = vmStat.Total / (1024 * 1024)
		m.memUsedMB = vmStat.Used / (1024 * 1024)
	}
	m.mu.Unlock()
}

// Snapshot returns a point-in-time copy of the current node metrics.
type Snapshot struct {
	CPUPercent float64
	MemPercent float64
	MemTotalMB uint64
	MemUsedMB  uint64
	UptimeSec  int64
}

func (m *NodeMonitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return Snapshot{
		CPUPercent: m.cpuPercent,
		MemPercent: m.memPercent,
		MemTotalMB: m.memTotalMB,
		MemUsedMB:  m.memUsedMB,
		UptimeSec:  int64(time.Since(m.startTime).Seconds()),
	}
}
