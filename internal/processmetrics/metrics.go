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

type Collector struct {
	mu          sync.Mutex
	clock       func() time.Time
	cpuSeconds  func() float64
	rss         func() uint64
	previousAt  time.Time
	previousCPU float64
	usage       float64
}

func New() *Collector {
	return newCollector(time.Now, runtimeCPUSeconds, residentMemory)
}

func newCollector(clock func() time.Time, cpuSeconds func() float64, rss func() uint64) *Collector {
	return &Collector{clock: clock, cpuSeconds: cpuSeconds, rss: rss}
}

func (collector *Collector) Collect() Metrics {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	now, cpu := collector.clock(), collector.cpuSeconds()
	if !collector.previousAt.IsZero() {
		elapsed := now.Sub(collector.previousAt).Seconds()
		if elapsed >= 0.2 {
			usage := (cpu - collector.previousCPU) / elapsed * 100
			if usage > 0 {
				collector.usage = usage
			} else {
				collector.usage = 0
			}
			collector.previousAt, collector.previousCPU = now, cpu
		}
	} else {
		collector.previousAt, collector.previousCPU = now, cpu
	}
	return Metrics{RSSBytes: collector.rss(), CPUUsage: collector.usage}
}

func runtimeCPUSeconds() float64 {
	samples := []runtimemetrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}}
	runtimemetrics.Read(samples)
	return samples[0].Value.Float64()
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
