import {
  Box,
  HStack,
  Heading,
  Input,
  Spinner,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react";
import { useMemo, useState } from "react";

import { useFetch } from "../api/hooks";
import type { Process, Topinfo } from "../api/types";
import { formatBytesFromKiB, formatNumber } from "../dashboard/format";
import { ProcessDrawer } from "./ProcessDrawer";

type SortKey = "cpu_percent" | "memory_rss" | "name" | "pid";

const POLL_MS = 5_000;

const STATUS_META: Record<string, { label: string; color: string }> = {
  R: { label: "Running", color: "#10B981" },
  S: { label: "Sleeping", color: "#9CA3AF" },
  D: { label: "Disk wait", color: "#F59E0B" },
  T: { label: "Stopped", color: "#F59E0B" },
  Z: { label: "Zombie", color: "#EF4444" },
  I: { label: "Idle", color: "#6B7280" },
};

function statusMeta(s: string) {
  if (!s) return { label: "—", color: "#6B7280" };
  return STATUS_META[s[0].toUpperCase()] ?? { label: s, color: "#9CA3AF" };
}

export function Processes() {
  const res = useFetch<Topinfo>("/data/topinfo", POLL_MS);
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("cpu_percent");
  const [selectedPid, setSelectedPid] = useState<number | null>(null);

  const procs = useMemo(() => {
    const list = res.data?.Processes ?? [];

    const filtered = search
      ? list.filter(
          (p) =>
            p.name.toLowerCase().includes(search.toLowerCase()) ||
            p.cmdline.toLowerCase().includes(search.toLowerCase()) ||
            p.username.toLowerCase().includes(search.toLowerCase()) ||
            String(p.pid) === search,
        )
      : list;

    return [...filtered]
      .sort((a, b) => {
        switch (sortKey) {
          case "cpu_percent":
            return b.cpu_percent - a.cpu_percent;
          case "memory_rss":
            return b.memory_rss - a.memory_rss;
          case "name":
            return a.name.localeCompare(b.name);
          case "pid":
            return a.pid - b.pid;
        }
      })
      .slice(0, 200);
  }, [res.data, search, sortKey]);

  const totalRSS = res.data?.Memory?.Total;

  const selectedProcess = useMemo(() => {
    if (selectedPid == null) return null;
    return res.data?.Processes?.find((p) => p.pid === selectedPid) ?? null;
  }, [res.data, selectedPid]);

  return (
    <VStack align="stretch" gap="5">
      <VStack align="start" gap="1">
        <Heading size="md">Processes</Heading>
        <SummaryRow info={res.data} />
      </VStack>

      <HStack justify="space-between" wrap="wrap" gap="3">
        <Text fontSize="xs" color="fg.subtle" fontFamily="mono">
          Showing top {procs.length} of {res.data?.Processes?.length ?? 0} ·
          refreshes every {POLL_MS / 1000}s
        </Text>
        <Input
          placeholder="Search name, command, user, PID…"
          size="sm"
          w={{ base: "full", md: "320px" }}
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
        />
      </HStack>

      <Box
        bg="surface.panel"
        borderWidth="1px"
        borderColor="border.subtle"
        borderRadius="lg"
        overflow="hidden"
      >
        {res.error ? (
          <Box p="6" color="status.crit">
            Failed to load processes: {res.error.message}
          </Box>
        ) : res.loading && procs.length === 0 ? (
          <HStack p="6" gap="3">
            <Spinner size="sm" />
            <Text color="fg.muted">Loading…</Text>
          </HStack>
        ) : (
          <Box maxH="calc(100vh - 280px)" overflow="auto">
            <Table.Root size="sm" variant="line" stickyHeader>
              <Table.Header bg="surface.subtle">
                <Table.Row>
                  <Table.ColumnHeader w="6"></Table.ColumnHeader>
                  <SortableHeader
                    label="PID"
                    sortKey="pid"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <SortableHeader
                    label="Name"
                    sortKey="name"
                    current={sortKey}
                    onSort={setSortKey}
                  />
                  <Table.ColumnHeader>User</Table.ColumnHeader>
                  <SortableHeader
                    label="CPU"
                    sortKey="cpu_percent"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <SortableHeader
                    label="Memory"
                    sortKey="memory_rss"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <Table.ColumnHeader>Command</Table.ColumnHeader>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {procs.map((p) => (
                  <ProcessRow
                    key={p.pid}
                    p={p}
                    totalRSS={totalRSS}
                    selected={p.pid === selectedPid}
                    onSelect={() => setSelectedPid(p.pid)}
                  />
                ))}
              </Table.Body>
            </Table.Root>
          </Box>
        )}
      </Box>

      <ProcessDrawer
        process={selectedProcess}
        requestedPid={selectedPid}
        onClose={() => setSelectedPid(null)}
      />
    </VStack>
  );
}

function SummaryRow({ info }: { info: Topinfo | null }) {
  if (!info) {
    return (
      <Text fontSize="sm" color="fg.muted">
        Loading…
      </Text>
    );
  }

  const loads = info.Loads ?? [];

  return (
    <HStack gap="5" wrap="wrap">
      <Stat label="Uptime" value={formatUptime(info.Uptime)} />
      <Stat label="Processes" value={String(info.Processes?.length ?? 0)} />
      <Stat
        label="Load avg"
        value={
          loads.length === 3
            ? `${formatNumber(loads[0], 2)} · ${formatNumber(loads[1], 2)} · ${formatNumber(loads[2], 2)}`
            : "—"
        }
      />
      {info.Users != null ? (
        <Stat label="Users" value={String(info.Users)} />
      ) : null}
    </HStack>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <HStack gap="2" align="baseline">
      <Text
        fontSize="xs"
        color="fg.subtle"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        {label}
      </Text>
      <Text
        fontSize="sm"
        color="fg.default"
        fontFamily="mono"
        fontVariantNumeric="tabular-nums"
      >
        {value}
      </Text>
    </HStack>
  );
}

function ProcessRow({
  p,
  totalRSS,
  selected,
  onSelect,
}: {
  p: Process;
  totalRSS: number | undefined;
  selected: boolean;
  onSelect: () => void;
}) {
  const cpuColor = colorForCPU(p.cpu_percent);
  const memColor = colorForMem(p.memory_rss, totalRSS);
  const memRatio =
    totalRSS && totalRSS > 0
      ? Math.min(1, p.memory_rss / totalRSS)
      : Math.min(1, p.memory_rss / (4 * 1024 ** 2)); // fallback: 4 GiB expressed in KiB

  const status = statusMeta(p.status);

  return (
    <Table.Row
      cursor="pointer"
      onClick={onSelect}
      bg={selected ? "surface.subtle" : undefined}
      _hover={{ bg: "surface.subtle" }}
      transition="background 100ms ease"
    >
      <Table.Cell>
        <Box
          w="2"
          h="2"
          borderRadius="full"
          bg={status.color}
          title={status.label}
        />
      </Table.Cell>
      <Table.Cell
        textAlign="end"
        fontFamily="mono"
        fontVariantNumeric="tabular-nums"
        color="fg.muted"
      >
        {p.pid}
      </Table.Cell>
      <Table.Cell>
        <HStack gap="2">
          <Text fontWeight="medium" truncate maxW="20ch">
            {p.name}
          </Text>
          {p.container_id ? (
            <Box
              fontSize="2xs"
              fontFamily="mono"
              color="fg.muted"
              bg="surface.subtle"
              borderWidth="1px"
              borderColor="border.subtle"
              px="1.5"
              borderRadius="sm"
              title={`Container: ${p.container_id}`}
            >
              container
            </Box>
          ) : null}
        </HStack>
      </Table.Cell>
      <Table.Cell color="fg.muted" fontSize="sm">
        {p.username || "—"}
      </Table.Cell>
      <Table.Cell>
        <BarValue
          value={`${p.cpu_percent.toFixed(1)}%`}
          ratio={Math.min(1, p.cpu_percent / 100)}
          color={cpuColor}
        />
      </Table.Cell>
      <Table.Cell>
        <BarValue
          value={formatBytesFromKiB(p.memory_rss)}
          ratio={memRatio}
          color={memColor}
        />
      </Table.Cell>
      <Table.Cell color="fg.subtle" fontSize="xs" fontFamily="mono" maxW="44ch">
        <Text truncate title={p.cmdline || p.executable}>
          {p.cmdline || p.executable}
        </Text>
      </Table.Cell>
    </Table.Row>
  );
}

function BarValue({
  value,
  ratio,
  color,
}: {
  value: string;
  ratio: number;
  color: string;
}) {
  return (
    <VStack align="end" gap="1">
      <Text
        fontFamily="mono"
        fontVariantNumeric="tabular-nums"
        fontSize="sm"
        color="fg.default"
      >
        {value}
      </Text>
      <Box
        w="60px"
        h="3px"
        bg="surface.subtle"
        borderRadius="full"
        overflow="hidden"
      >
        <Box
          h="full"
          w={`${Math.max(2, ratio * 100)}%`}
          bg={color}
          borderRadius="full"
          transition="width 240ms ease"
        />
      </Box>
    </VStack>
  );
}

function colorForCPU(percent: number): string {
  if (percent >= 80) return "#EF4444"; // crit
  if (percent >= 40) return "#F59E0B"; // warn
  return "#4F8DF5"; // accent
}

function colorForMem(rss: number, total: number | undefined): string {
  if (!total || total <= 0) return "#8B5CF6";
  const r = rss / total;
  if (r >= 0.25) return "#EF4444";
  if (r >= 0.1) return "#F59E0B";
  return "#8B5CF6";
}

function SortableHeader({
  label,
  sortKey,
  current,
  onSort,
  textAlign,
}: {
  label: string;
  sortKey: SortKey;
  current: SortKey;
  onSort: (k: SortKey) => void;
  textAlign?: "start" | "end";
}) {
  const active = current === sortKey;

  return (
    <Table.ColumnHeader
      textAlign={textAlign}
      cursor="pointer"
      onClick={() => onSort(sortKey)}
      _hover={{ color: "fg.default" }}
    >
      <Text
        as="span"
        color={active ? "fg.default" : "fg.muted"}
        fontWeight={active ? "semibold" : "medium"}
      >
        {label}
        {active ? " ↓" : ""}
      </Text>
    </Table.ColumnHeader>
  );
}

function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0) return "—";

  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const mins = Math.floor((seconds % 3_600) / 60);

  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;

  return `${mins}m`;
}
