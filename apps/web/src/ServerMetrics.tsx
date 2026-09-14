import { api, type ApiServerMetrics } from "@servediff/api";
import { useEffect, useState } from "react";
import { formatBytes } from "./server-metrics.ts";

const refreshMilliseconds = 2_500;

export function ServerMetrics() {
  const [metrics, setMetrics] = useState<ApiServerMetrics | null>(null);
  useEffect(() => {
    let disposed = false;
    let controller = new AbortController();
    async function refresh() {
      controller.abort();
      controller = new AbortController();
      try {
        const { data } = await api.GET("/api/v1/metrics", {
          signal: controller.signal,
        });
        if (!disposed && data) setMetrics(data);
      } catch {}
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), refreshMilliseconds);
    return () => {
      disposed = true;
      controller.abort();
      window.clearInterval(timer);
    };
  }, []);

  const cpu = metrics ? `${metrics.cpuUsage.toFixed(1)}%` : "--%";
  const ram = metrics ? formatBytes(metrics.rssBytes) : "-- MB";
  const title = metrics
    ? `ServeDiff server process\nResident RAM (RSS): ${ram}\nCPU usage: ${cpu}`
    : "ServeDiff server process metrics";
  return (
    <span id="server-metrics" className="server-metrics" title={title}>
      <span>
        <span className="server-metrics-label">CPU </span>
        <span id="server-cpu">{cpu}</span>
      </span>
      <span className="server-metrics-divider" aria-hidden="true">
        ·
      </span>
      <span>
        <span className="server-metrics-label">RAM </span>
        <span id="server-ram">{ram}</span>
      </span>
    </span>
  );
}
