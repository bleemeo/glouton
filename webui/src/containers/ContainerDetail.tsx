import {
  Heading,
  HStack,
  SimpleGrid,
  Spinner,
  Text,
  VStack,
} from "@chakra-ui/react";
import { useCallback, useMemo } from "react";
import { LuArrowLeft } from "react-icons/lu";
import {
  Link as RouterLink,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";

import { useFetch, usePromQLRange, useStoreInfo } from "../api/hooks";
import type { Container, ContainersResponse } from "../api/types";
import { StatusBadge, type Status } from "../app/StatusBadge";
import {
  formatBitsPerSec,
  formatBytes,
  formatPercent,
} from "../dashboard/format";
import { MetricChart, type ChartSeries } from "../dashboard/MetricChart";
import { alignSeries } from "../dashboard/promql";
import { DEFAULT_RANGE_ID, RANGES, type Range } from "../dashboard/ranges";
import { RangeSelector } from "../dashboard/RangeSelector";

const RANGE_PARAM = "range";

const COLORS = {
  cpu: "#4F8DF5",
  mem: "#8B5CF6",
  netRecv: "#4F8DF5",
  netSent: "#F97316",
  ioRead: "#4F8DF5",
  ioWrite: "#F97316",
};

function stateToStatus(state: string): Status {
  const s = state.toLowerCase();
  if (s.includes("running") || s === "up") return "ok";
  if (s.includes("paused") || s.includes("restarting")) return "warn";
  if (s.includes("exited") || s.includes("dead") || s.includes("stopped"))
    return "crit";
  return "unknown";
}

export function ContainerDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();

  const decodedName = name ? decodeURIComponent(name) : "";

  // Fetch the full container list and pluck the matching one. This
  // keeps a single source of truth (drawer + detail page see the
  // same record) and means the detail page also auto-updates as the
  // container's runtime stats refresh.
  const list = useFetch<ContainersResponse>(
    `/data/docker-containers?limit=200&allContainers=true`,
    15_000,
  );
  const container = useMemo(
    () => list.data?.containers.find((c) => c.name === decodedName) ?? null,
    [list.data, decodedName],
  );

  const storeInfo = useStoreInfo();

  const [searchParams, setSearchParams] = useSearchParams();
  const range = useMemo<Range>(() => {
    const id = searchParams.get(RANGE_PARAM);
    return (
      RANGES.find((r) => r.id === id) ??
      RANGES.find((r) => r.id === DEFAULT_RANGE_ID) ??
      RANGES[0]
    );
  }, [searchParams]);

  const setRange = useCallback(
    (next: Range) => {
      setSearchParams(
        (prev) => {
          const params = new URLSearchParams(prev);
          if (next.id === DEFAULT_RANGE_ID) {
            params.delete(RANGE_PARAM);
          } else {
            params.set(RANGE_PARAM, next.id);
          }
          return params;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  if (list.loading && !container) {
    return (
      <HStack p="4" gap="3">
        <Spinner size="sm" />
        <Text color="fg.muted">Loading container…</Text>
      </HStack>
    );
  }

  if (!container) {
    return (
      <VStack align="start" gap="3">
        <Heading size="md">Container not found</Heading>
        <Text color="fg.muted">
          No container named &quot;{decodedName}&quot; is currently running. It
          may have been stopped or renamed.
        </Text>
        <BackLink onClick={() => navigate("/containers")} />
      </VStack>
    );
  }

  return (
    <VStack align="stretch" gap="6">
      <BackLink onClick={() => navigate("/containers")} />

      <Header container={container} />

      <RangeSelector
        selectedId={range.id}
        onSelect={setRange}
        storeInfo={storeInfo.data}
      />

      <SimpleGrid columns={{ base: 1, lg: 2 }} gap="3">
        <ContainerCPUChart name={decodedName} range={range} />
        <ContainerMemChart name={decodedName} range={range} />
        <ContainerNetChart name={decodedName} range={range} />
        <ContainerIOChart name={decodedName} range={range} />
      </SimpleGrid>
    </VStack>
  );
}

function BackLink({ onClick }: { onClick: () => void }) {
  return (
    <RouterLink
      to="/containers"
      onClick={(e) => {
        e.preventDefault();
        onClick();
      }}
      style={{ textDecoration: "none" }}
    >
      <HStack gap="1" color="fg.muted" _hover={{ color: "fg.default" }}>
        <LuArrowLeft />
        <Text fontSize="sm">Back to containers</Text>
      </HStack>
    </RouterLink>
  );
}

function Header({ container: c }: { container: Container }) {
  return (
    <VStack align="start" gap="2">
      <HStack gap="3" wrap="wrap">
        <StatusBadge status={stateToStatus(c.state)} label={c.state} />
        <Heading size="md">{c.name}</Heading>
      </HStack>
      <HStack
        gap="6"
        wrap="wrap"
        fontSize="xs"
        color="fg.muted"
        fontFamily="mono"
      >
        <Text>{c.image}</Text>
        <Text title={c.id}>id: {c.id.slice(0, 12)}</Text>
        {c.command ? (
          <Text truncate maxW="60ch" title={c.command}>
            cmd: {c.command}
          </Text>
        ) : null}
      </HStack>
    </VStack>
  );
}

function escapeLabelValue(v: string): string {
  return v.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function ContainerCPUChart({ name, range }: { name: string; range: Range }) {
  const filter = `{item="${escapeLabelValue(name)}"}`;
  const cpu = usePromQLRange(
    `container_cpu_used${filter}`,
    range.seconds,
    range.step,
  );

  const data = alignSeries({ cpu: cpu.data?.data?.result?.[0] });
  const series: ChartSeries[] = [
    { key: "cpu", label: "CPU", color: COLORS.cpu },
  ];

  return (
    <MetricChart
      title="CPU usage"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => formatPercent(v)}
      loading={cpu.loading}
      error={cpu.error}
    />
  );
}

function ContainerMemChart({ name, range }: { name: string; range: Range }) {
  const filter = `{item="${escapeLabelValue(name)}"}`;
  const mem = usePromQLRange(
    `container_mem_used_perc${filter}`,
    range.seconds,
    range.step,
  );

  const data = alignSeries({ mem: mem.data?.data?.result?.[0] });
  const series: ChartSeries[] = [
    { key: "mem", label: "Memory", color: COLORS.mem },
  ];

  return (
    <MetricChart
      title="Memory usage"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      yDomain={[0, 100]}
      formatValue={(v) => formatPercent(v)}
      loading={mem.loading}
      error={mem.error}
    />
  );
}

function ContainerNetChart({ name, range }: { name: string; range: Range }) {
  const filter = `{item="${escapeLabelValue(name)}"}`;
  const recv = usePromQLRange(
    `container_net_bits_recv${filter}`,
    range.seconds,
    range.step,
  );
  const sent = usePromQLRange(
    `container_net_bits_sent${filter}`,
    range.seconds,
    range.step,
  );

  const data = alignSeries({
    recv: recv.data?.data?.result?.[0],
    sent: sent.data?.data?.result?.[0],
  });
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
    />
  );
}

function ContainerIOChart({ name, range }: { name: string; range: Range }) {
  const filter = `{item="${escapeLabelValue(name)}"}`;
  const read = usePromQLRange(
    `container_io_read_bytes${filter}`,
    range.seconds,
    range.step,
  );
  const write = usePromQLRange(
    `container_io_write_bytes${filter}`,
    range.seconds,
    range.step,
  );

  const data = alignSeries({
    read: read.data?.data?.result?.[0],
    write: write.data?.data?.result?.[0],
  });
  const series: ChartSeries[] = [
    { key: "read", label: "Read", color: COLORS.ioRead },
    { key: "write", label: "Write", color: COLORS.ioWrite },
  ];

  return (
    <MetricChart
      title="Disk I/O"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      formatValue={(v) => `${formatBytes(v)}/s`}
      loading={read.loading}
      error={read.error ?? write.error}
    />
  );
}
