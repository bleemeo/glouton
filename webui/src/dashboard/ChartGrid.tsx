import { SimpleGrid } from "@chakra-ui/react";

import { usePromQLRange } from "../api/hooks";
import { formatBytes, formatNumber, formatPercent } from "./format";
import { MetricChart, type ChartSeries } from "./MetricChart";
import { alignSeries } from "./promql";
import type { Range } from "./ranges";

const COLORS = {
  cpuUser: "#4F8DF5",
  cpuSystem: "#8B5CF6",
  cpuWait: "#F97316",
  cpuOther: "#9CA3AF",
  memUsed: "#4F8DF5",
  memCached: "#8B5CF6",
  memFree: "#34D399",
  swapUsed: "#F97316",
  swapFree: "#34D399",
  load1: "#EF4444",
  load5: "#F59E0B",
  load15: "#10B981",
};

export function ChartGrid({ range }: { range: Range }) {
  return (
    <SimpleGrid columns={{ base: 1, lg: 2 }} gap="3">
      <CPUChart range={range} />
      <LoadChart range={range} />
      <MemoryChart range={range} />
      <SwapChart range={range} />
    </SimpleGrid>
  );
}

function CPUChart({ range }: { range: Range }) {
  const user = usePromQLRange("cpu_user", range.seconds, range.step);
  const system = usePromQLRange("cpu_system", range.seconds, range.step);
  const wait = usePromQLRange("cpu_wait", range.seconds, range.step);
  const other = usePromQLRange("cpu_other", range.seconds, range.step);

  const data = alignSeries({
    user: user.data?.data?.result?.[0],
    system: system.data?.data?.result?.[0],
    wait: wait.data?.data?.result?.[0],
    other: other.data?.data?.result?.[0],
  });

  const series: ChartSeries[] = [
    { key: "user", label: "User", color: COLORS.cpuUser, stack: "cpu" },
    { key: "system", label: "System", color: COLORS.cpuSystem, stack: "cpu" },
    { key: "wait", label: "I/O wait", color: COLORS.cpuWait, stack: "cpu" },
    { key: "other", label: "Other", color: COLORS.cpuOther, stack: "cpu" },
  ];

  return (
    <MetricChart
      title="CPU usage"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      yDomain={[0, 100]}
      formatValue={(v) => formatPercent(v)}
      loading={user.loading || system.loading}
      error={user.error ?? system.error}
    />
  );
}

function LoadChart({ range }: { range: Range }) {
  const l1 = usePromQLRange("system_load1", range.seconds, range.step);
  const l5 = usePromQLRange("system_load5", range.seconds, range.step);
  const l15 = usePromQLRange("system_load15", range.seconds, range.step);

  const data = alignSeries({
    load1: l1.data?.data?.result?.[0],
    load5: l5.data?.data?.result?.[0],
    load15: l15.data?.data?.result?.[0],
  });

  const series: ChartSeries[] = [
    { key: "load1", label: "1 min", color: COLORS.load1 },
    { key: "load5", label: "5 min", color: COLORS.load5 },
    { key: "load15", label: "15 min", color: COLORS.load15 },
  ];

  return (
    <MetricChart
      title="Load average"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => formatNumber(v, 2)}
      loading={l1.loading}
      error={l1.error ?? l5.error ?? l15.error}
      variant="line"
    />
  );
}

function MemoryChart({ range }: { range: Range }) {
  const used = usePromQLRange("mem_used", range.seconds, range.step);
  const cached = usePromQLRange("mem_cached", range.seconds, range.step);
  const free = usePromQLRange("mem_free", range.seconds, range.step);

  const data = alignSeries({
    used: used.data?.data?.result?.[0],
    cached: cached.data?.data?.result?.[0],
    free: free.data?.data?.result?.[0],
  });

  const series: ChartSeries[] = [
    { key: "used", label: "Used", color: COLORS.memUsed, stack: "mem" },
    { key: "cached", label: "Cached", color: COLORS.memCached, stack: "mem" },
    { key: "free", label: "Free", color: COLORS.memFree, stack: "mem" },
  ];

  return (
    <MetricChart
      title="Memory"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => formatBytes(v)}
      loading={used.loading}
      error={used.error}
    />
  );
}

function SwapChart({ range }: { range: Range }) {
  const used = usePromQLRange("swap_used", range.seconds, range.step);
  const free = usePromQLRange("swap_free", range.seconds, range.step);

  const data = alignSeries({
    used: used.data?.data?.result?.[0],
    free: free.data?.data?.result?.[0],
  });

  const series: ChartSeries[] = [
    { key: "used", label: "Used", color: COLORS.swapUsed, stack: "swap" },
    { key: "free", label: "Free", color: COLORS.swapFree, stack: "swap" },
  ];

  return (
    <MetricChart
      title="Swap"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => formatBytes(v)}
      loading={used.loading}
      error={used.error}
    />
  );
}
