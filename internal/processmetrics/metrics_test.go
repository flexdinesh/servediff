package processmetrics

import (
	"testing"
	"time"
)

func TestCollectorSamplesCPUOverWallTime(t *testing.T) {
	times := []time.Time{time.Unix(0, 0), time.Unix(0, 100_000_000), time.Unix(1, 0)}
	cpus := []float64{0.15, 0.20, 0.40}
	rss := []uint64{10, 20, 30}
	index := 0
	collector := newCollector(func() time.Time { return times[index] }, func() float64 { return cpus[index] }, func() uint64 { return rss[index] })
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
