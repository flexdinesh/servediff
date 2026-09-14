interface CpuTime {
  user: number;
  system: number;
}

export interface ServerMetrics {
  rssBytes: number;
  cpuUsage: number;
}

const minimumSampleNanoseconds = 200_000_000n;

export function createProcessMetricsCollector(
  dependencies: {
    clock?: () => bigint;
    cpu?: () => CpuTime;
    rss?: () => number;
  } = {},
) {
  const clock = dependencies.clock ?? process.hrtime.bigint;
  const cpu = dependencies.cpu ?? process.cpuUsage;
  const rss = dependencies.rss ?? process.memoryUsage.rss;
  let previousWall: bigint | null = null;
  let previousCpu: CpuTime | null = null;
  let cpuUsage = 0;

  return (): ServerMetrics => {
    const wall = clock();
    const cpuTime = cpu();
    if (previousWall === null || previousCpu === null) {
      previousWall = wall;
      previousCpu = cpuTime;
    } else {
      const wallDelta = wall - previousWall;
      if (wallDelta >= minimumSampleNanoseconds) {
        const cpuMicroseconds =
          cpuTime.user - previousCpu.user + cpuTime.system - previousCpu.system;
        const usage = (cpuMicroseconds / (Number(wallDelta) / 1_000)) * 100;
        cpuUsage = Number.isFinite(usage) && usage > 0 ? usage : 0;
        previousWall = wall;
        previousCpu = cpuTime;
      }
    }
    return { rssBytes: rss(), cpuUsage };
  };
}

export const getProcessMetrics = createProcessMetricsCollector();
