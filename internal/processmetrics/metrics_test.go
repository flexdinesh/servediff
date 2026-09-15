package processmetrics

import (
	"testing"
	"time"
)

func TestCollectorSamplesBusyCPUCapacity(t *testing.T) {
	times := []time.Time{time.Unix(0, 0), time.Unix(0, 100_000_000), time.Unix(1, 0)}
	cpus := []cpuTimes{{total: 4, idle: 3}, {total: 4.4, idle: 3.3}, {total: 8, idle: 6}}
	rss := []uint64{10, 20, 30}
	index := 0
	collector := newCollector(func() time.Time { return times[index] }, func() cpuTimes { return cpus[index] }, func() uint64 { return rss[index] })
	if value := collector.Collect(); value.RSSBytes != 10 || value.CPUUsage != 0 {
		t.Fatalf("first sample: %#v", value)
	}
	index++
	if value := collector.Collect(); value.RSSBytes != 20 || value.CPUUsage != 0 {
		t.Fatalf("short sample: %#v", value)
	}
	index++
	if value := collector.Collect(); value.RSSBytes != 30 || value.CPUUsage < 24.9 || value.CPUUsage > 25.1 {
		t.Fatalf("sample: %#v", value)
	}
}

func TestCPUUsageClampsRuntimeEstimationNoise(t *testing.T) {
	for name, test := range map[string]struct {
		previous cpuTimes
		current  cpuTimes
		expected float64
	}{
		"negative": {previous: cpuTimes{total: 4, idle: 3}, current: cpuTimes{total: 8, idle: 8}, expected: 0},
		"over":     {previous: cpuTimes{total: 4, idle: 3}, current: cpuTimes{total: 8, idle: 2}, expected: 100},
		"reset":    {previous: cpuTimes{total: 4, idle: 3}, current: cpuTimes{total: 2, idle: 1}, expected: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if actual := cpuUsage(test.previous, test.current); actual != test.expected {
				t.Fatalf("got %f, want %f", actual, test.expected)
			}
		})
	}
}
