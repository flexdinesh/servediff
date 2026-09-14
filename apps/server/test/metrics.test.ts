import assert from "node:assert/strict";
import test from "node:test";
import { createProcessMetricsCollector } from "../src/metrics.ts";

test("measures process CPU over elapsed wall time", () => {
  const walls = [0n, 100_000_000n, 1_000_000_000n];
  const cpuTimes = [
    { user: 100_000, system: 50_000 },
    { user: 140_000, system: 60_000 },
    { user: 300_000, system: 100_000 },
  ];
  const rssValues = [10, 20, 30];
  let index = 0;
  const collect = createProcessMetricsCollector({
    clock: () => walls[index] ?? 0n,
    cpu: () => cpuTimes[index] ?? { user: 0, system: 0 },
    rss: () => rssValues[index] ?? 0,
  });

  assert.deepEqual(collect(), { rssBytes: 10, cpuUsage: 0 });
  index++;
  assert.deepEqual(collect(), { rssBytes: 20, cpuUsage: 0 });
  index++;
  assert.deepEqual(collect(), { rssBytes: 30, cpuUsage: 25 });
});
