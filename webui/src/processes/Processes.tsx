import {
  Box,
  Heading,
  HStack,
  Input,
  Spinner,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react";
import { useMemo, useState } from "react";

import { useFetch } from "../api/hooks";
import type { Topinfo } from "../api/types";
import { formatBytes } from "../dashboard/format";

type SortKey = "cpu_percent" | "memory_rss" | "name" | "pid";

export function Processes() {
  const res = useFetch<Topinfo>("/data/topinfo", 5_000);
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("cpu_percent");

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

    const sorted = [...filtered].sort((a, b) => {
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
    });

    return sorted.slice(0, 200);
  }, [res.data, search, sortKey]);

  return (
    <VStack align="stretch" gap="4">
      <HStack justify="space-between" wrap="wrap" gap="3">
        <VStack align="start" gap="0">
          <Heading size="md">Processes</Heading>
          <Text fontSize="sm" color="fg.muted">
            {res.data
              ? `Showing top ${procs.length} of ${res.data.Processes?.length ?? 0} (uptime ${formatUptime(res.data.Uptime)})`
              : "Loading…"}
          </Text>
        </VStack>

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
          <Table.Root size="sm" variant="line">
            <Table.Header bg="surface.subtle">
              <Table.Row>
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
                  label="Memory (RSS)"
                  sortKey="memory_rss"
                  current={sortKey}
                  onSort={setSortKey}
                  textAlign="end"
                />
                <Table.ColumnHeader>Status</Table.ColumnHeader>
                <Table.ColumnHeader>Command</Table.ColumnHeader>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {procs.map((p) => (
                <Table.Row key={p.pid}>
                  <Table.Cell
                    textAlign="end"
                    fontFamily="mono"
                    fontVariantNumeric="tabular-nums"
                    color="fg.muted"
                  >
                    {p.pid}
                  </Table.Cell>
                  <Table.Cell fontWeight="medium">{p.name}</Table.Cell>
                  <Table.Cell color="fg.muted" fontSize="sm">
                    {p.username || "—"}
                  </Table.Cell>
                  <Table.Cell textAlign="end" fontFamily="mono" fontVariantNumeric="tabular-nums">
                    {p.cpu_percent.toFixed(1)}%
                  </Table.Cell>
                  <Table.Cell textAlign="end" fontFamily="mono" fontVariantNumeric="tabular-nums">
                    {formatBytes(p.memory_rss)}
                  </Table.Cell>
                  <Table.Cell color="fg.muted" fontSize="xs">
                    {p.status}
                  </Table.Cell>
                  <Table.Cell color="fg.subtle" fontSize="xs" fontFamily="mono" maxW="48ch">
                    <Text truncate>{p.cmdline || p.executable}</Text>
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Root>
        )}
      </Box>
    </VStack>
  );
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
    <Table.ColumnHeader textAlign={textAlign} cursor="pointer" onClick={() => onSort(sortKey)}>
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
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const mins = Math.floor((seconds % 3_600) / 60);

  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;

  return `${mins}m`;
}
