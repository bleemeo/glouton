import { SimpleGrid } from "@chakra-ui/react";
import { useState } from "react";

import { usePromQLRange } from "../api/hooks";
import { DeviceSelect } from "./DeviceSelect";
import { formatBitsPerSec, formatBytes, formatNumber, formatPercent } from "./format";
import { MetricChart, type ChartSeries } from "./MetricChart";
import { alignSeries, seriesByLabel, sumSeries } from "./promql";
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
  netRecv: "#4F8DF5",
  netSent: "#F97316",
  ioRead: "#4F8DF5",
  ioWrite: "#F97316",
};

// Cycled when the chart has dynamic series (one per device, mount, etc).
const DYNAMIC_PALETTE = [
  "#4F8DF5",
  "#8B5CF6",
  "#F97316",
  "#10B981",
  "#EF4444",
  "#F59E0B",
  "#06B6D4",
  "#EC4899",
];

export function SystemChartGrid({ range }: { range: Range }) {
  return (
    <SimpleGrid columns={{ base: 1, lg: 2 }} gap="3">
      <CPUChart range={range} />
      <LoadChart range={range} />
      <MemoryChart range={range} />
      <SwapChart range={range} />
    </SimpleGrid>
  );
}

export function NetworkAndIOChartGrid({ range }: { range: Range }) {
  return (
    <SimpleGrid columns={{ base: 1, lg: 2 }} gap="3">
      <DiskUtilizationChart range={range} />
      <FilesystemChart range={range} />
      <NetworkChart range={range} />
      <DiskIOChart range={range} />
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

function NetworkChart({ range }: { range: Range }) {
  // Fetch per-interface so the dropdown can offer the list of items
  // and so client-side summation can replay the "All" view without a
  // second round-trip.
  const recv = usePromQLRange("net_bits_recv", range.seconds, range.step);
  const sent = usePromQLRange("net_bits_sent", range.seconds, range.step);

  const recvByItem = seriesByLabel(recv.data, "item");
  const sentByItem = seriesByLabel(sent.data, "item");
  const items = Array.from(
    new Set([...Object.keys(recvByItem), ...Object.keys(sentByItem)]),
  ).sort();

  const [selected, setSelected] = useState("");

  const recvSeries =
    selected === ""
      ? sumSeries(Object.values(recvByItem))
      : recvByItem[selected];
  const sentSeries =
    selected === ""
      ? sumSeries(Object.values(sentByItem))
      : sentByItem[selected];

  const data = alignSeries({ recv: recvSeries, sent: sentSeries });

  const series: ChartSeries[] = [
    { key: "recv", label: "↓ Received", color: COLORS.netRecv },
    { key: "sent", label: "↑ Sent", color: COLORS.netSent },
  ];

  return (
    <MetricChart
      title="Network throughput"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => formatBitsPerSec(v)}
      loading={recv.loading}
      error={recv.error ?? sent.error}
      controls={
        items.length > 0 ? (
          <DeviceSelect
            value={selected}
            onChange={setSelected}
            options={items}
            allLabel="All interfaces"
          />
        ) : null
      }
    />
  );
}

function DiskIOChart({ range }: { range: Range }) {
  const read = usePromQLRange("io_read_bytes", range.seconds, range.step);
  const write = usePromQLRange("io_write_bytes", range.seconds, range.step);

  const readByItem = seriesByLabel(read.data, "item");
  const writeByItem = seriesByLabel(write.data, "item");
  const items = Array.from(
    new Set([...Object.keys(readByItem), ...Object.keys(writeByItem)]),
  ).sort();

  const [selected, setSelected] = useState("");

  const readSeries =
    selected === ""
      ? sumSeries(Object.values(readByItem))
      : readByItem[selected];
  const writeSeries =
    selected === ""
      ? sumSeries(Object.values(writeByItem))
      : writeByItem[selected];

  const data = alignSeries({ read: readSeries, write: writeSeries });

  const series: ChartSeries[] = [
    { key: "read", label: "Read", color: COLORS.ioRead },
    { key: "write", label: "Write", color: COLORS.ioWrite },
  ];

  return (
    <MetricChart
      title="Disk I/O throughput"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => `${formatBytes(v)}/s`}
      loading={read.loading}
      error={read.error ?? write.error}
      controls={
        items.length > 0 ? (
          <DeviceSelect
            value={selected}
            onChange={setSelected}
            options={items}
            allLabel="All devices"
          />
        ) : null
      }
    />
  );
}

function DiskUtilizationChart({ range }: { range: Range }) {
  const res = usePromQLRange("io_utilization", range.seconds, range.step);

  const labelled = seriesByLabel(res.data, "item");
  const sortedKeys = Object.keys(labelled).sort();

  const data = alignSeries(
    Object.fromEntries(sortedKeys.map((k) => [k, labelled[k]])),
  );

  const series: ChartSeries[] = sortedKeys.map((key, i) => ({
    key,
    label: key,
    color: DYNAMIC_PALETTE[i % DYNAMIC_PALETTE.length],
  }));

  return (
    <MetricChart
      title="Disk utilization"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      yDomain={[0, 100]}
      formatValue={(v) => formatPercent(v)}
      loading={res.loading}
      error={res.error}
      variant="line"
    />
  );
}

function FilesystemChart({ range }: { range: Range }) {
  const res = usePromQLRange("disk_used_perc", range.seconds, range.step);

  // The same physical volume often appears under multiple synthetic
  // mountpoints on macOS (System/Volumes/...). To keep the chart
  // readable, deduplicate by the current value and keep only mounts
  // whose usage differs from neighbours, capping the chart to ~6 lines.
  const labelled = seriesByLabel(res.data, "item");
  const trimmed = topMounts(labelled, 6);

  const data = alignSeries(trimmed);

  const series: ChartSeries[] = Object.keys(trimmed).map((key, i) => ({
    key,
    label: key,
    color: DYNAMIC_PALETTE[i % DYNAMIC_PALETTE.length],
  }));

  return (
    <MetricChart
      title="Filesystem usage"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      yDomain={[0, 100]}
      formatValue={(v) => formatPercent(v)}
      loading={res.loading}
      error={res.error}
      variant="line"
    />
  );
}

// topMounts collapses near-duplicate mountpoints (same current usage to
// 0.1% precision) and returns at most max entries, sorted by current
// usage descending so the busiest filesystems show up first.
function topMounts<T extends { values: Array<[number, string]> }>(
  series: Record<string, T>,
  max: number,
): Record<string, T> {
  type Entry = { key: string; value: T; last: number };

  const entries: Entry[] = Object.entries(series).map(([key, value]) => {
    const samples = value.values;
    const lastRaw = samples.length > 0 ? samples[samples.length - 1][1] : "0";
    const last = parseFloat(lastRaw);

    return { key, value, last: isFinite(last) ? last : 0 };
  });

  // Deduplicate by rounded current value; "/" wins over "/System/Volumes/Data".
  const seen = new Set<number>();
  const dedup: Entry[] = [];

  for (const entry of entries.sort((a, b) => a.key.length - b.key.length)) {
    const bucket = Math.round(entry.last * 10);
    if (seen.has(bucket)) continue;
    seen.add(bucket);
    dedup.push(entry);
  }

  dedup.sort((a, b) => b.last - a.last);

  return Object.fromEntries(dedup.slice(0, max).map((e) => [e.key, e.value]));
}
