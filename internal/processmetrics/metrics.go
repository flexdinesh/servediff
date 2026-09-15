package processmetrics

import (
	"os"
	"runtime"
	runtimemetrics "runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Metrics struct {
	RSSBytes uint64  `json:"rssBytes"`
	CPUUsage float64 `json:"cpuUsage"`
}

type cpuTimes struct {
	total float64
	idle  float64
}

type Collector struct {
	mu          sync.Mutex
	clock       func() time.Time
	cpuTimes    func() cpuTimes
	rss         func() uint64
	previousAt  time.Time
	previousCPU cpuTimes
	usage       float64
}

func New() *Collector {
	return newCollector(time.Now, runtimeCPUTimes, residentMemory)
}

func newCollector(clock func() time.Time, cpuTimes func() cpuTimes, rss func() uint64) *Collector {
	return &Collector{clock: clock, cpuTimes: cpuTimes, rss: rss}
}

func (collector *Collector) Collect() Metrics {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	now, cpu := collector.clock(), collector.cpuTimes()
	if !collector.previousAt.IsZero() {
		elapsed := now.Sub(collector.previousAt).Seconds()
		if elapsed >= 0.2 {
			collector.usage = cpuUsage(collector.previousCPU, cpu)
			collector.previousAt, collector.previousCPU = now, cpu
		}
	} else {
		collector.previousAt, collector.previousCPU = now, cpu
	}
	return Metrics{RSSBytes: collector.rss(), CPUUsage: collector.usage}
}

func cpuUsage(previous, current cpuTimes) float64 {
	total := current.total - previous.total
	if total <= 0 {
		return 0
	}
	idle := current.idle - previous.idle
	return min(max((total-idle)/total*100, 0), 100)
}

func runtimeCPUTimes() cpuTimes {
	samples := []runtimemetrics.Sample{
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/cpu/classes/idle:cpu-seconds"},
	}
	runtimemetrics.Read(samples)
	return cpuTimes{total: samples[0].Value.Float64(), idle: samples[1].Value.Float64()}
}

func residentMemory() uint64 {
	if raw, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) > 1 {
			pages, parseError := strconv.ParseUint(fields[1], 10, 64)
			if parseError == nil {
				return pages * uint64(os.Getpagesize())
			}
		}
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return memory.Sys
}
