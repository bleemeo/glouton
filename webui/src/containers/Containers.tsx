import {
  Box,
  chakra,
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
import type { ContainersResponse } from "../api/types";
import { StatusBadge, type Status } from "../app/StatusBadge";
import { formatBytes } from "../dashboard/format";
import { ContainerDrawer } from "./ContainerDrawer";

function stateToStatus(state: string): Status {
  const s = state.toLowerCase();

  if (s.includes("running") || s === "up") return "ok";
  if (s.includes("paused") || s.includes("restarting")) return "warn";
  if (s.includes("exited") || s.includes("dead") || s.includes("stopped"))
    return "crit";

  return "unknown";
}

type SortKey =
  | "state"
  | "name"
  | "image"
  | "cpu"
  | "memory"
  | "netRecv"
  | "netSent"
  | "started";

function startedTimestamp(iso: string | undefined): number {
  if (!iso) return 0;

  const t = new Date(iso).getTime();

  return isFinite(t) ? t : 0;
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

function relativeTime(iso: string | undefined): string {
  if (!iso) return "—";

  const t = new Date(iso).getTime();

  if (!isFinite(t) || t === new Date("0001-01-01T00:00:00Z").getTime())
    return "—";

  const diffSec = Math.floor((Date.now() - t) / 1000);

  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;

  return `${Math.floor(diffSec / 86400)}d ago`;
}

export function Containers() {
  const [search, setSearch] = useState("");
  const [allContainers, setAllContainers] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>("name");

  const url = `/data/docker-containers?limit=200&allContainers=${allContainers}&search=${encodeURIComponent(search)}`;
  const res = useFetch<ContainersResponse>(url, 15_000);

  const items = useMemo(() => {
    const rows = res.data?.containers ?? [];

    return [...rows].sort((a, b) => {
      switch (sortKey) {
        case "state":
          return a.state.localeCompare(b.state);
        case "name":
          return a.name.localeCompare(b.name);
        case "image":
          return a.image.localeCompare(b.image);
        case "cpu":
          return b.cpuUsedPerc - a.cpuUsedPerc;
        case "memory":
          return b.memUsedPerc - a.memUsedPerc;
        case "netRecv":
          return b.netBitsRecv - a.netBitsRecv;
        case "netSent":
          return b.netBitsSent - a.netBitsSent;
        case "started":
          return startedTimestamp(b.startedAt) - startedTimestamp(a.startedAt);
      }
    });
  }, [res.data, sortKey]);

  const selectedContainer = useMemo(() => {
    if (!selectedId) return null;
    return items.find((c) => c.id === selectedId) ?? null;
  }, [items, selectedId]);

  return (
    <VStack align="stretch" gap="4">
      <HStack justify="space-between" wrap="wrap" gap="3">
        <VStack align="start" gap="0">
          <Heading size="md">Containers</Heading>
          <Text fontSize="sm" color="fg.muted">
            {res.data
              ? `${res.data.currentCount} shown · ${res.data.count} ${
                  allContainers ? "total" : "running"
                }`
              : "Loading…"}
          </Text>
        </VStack>

        <HStack gap="2">
          <Input
            placeholder="Search name, image, ID, command…"
            size="sm"
            w={{ base: "full", md: "320px" }}
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
          <chakra.button
            type="button"
            onClick={() => setAllContainers((v) => !v)}
            px="3"
            py="1.5"
            fontSize="sm"
            fontWeight="medium"
            borderRadius="md"
            borderWidth="1px"
            borderColor="border.default"
            bg={allContainers ? "surface.subtle" : "surface.panel"}
            color="fg.default"
            cursor="pointer"
            _hover={{ bg: "surface.subtle" }}
          >
            {allContainers ? "All" : "Running only"}
          </chakra.button>
        </HStack>
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
            Failed to load containers: {res.error.message}
          </Box>
        ) : res.loading && items.length === 0 ? (
          <HStack p="6" gap="3">
            <Spinner size="sm" />
            <Text color="fg.muted">Loading…</Text>
          </HStack>
        ) : items.length === 0 ? (
          <Box p="6" color="fg.muted">
            No containers match.
          </Box>
        ) : (
          <Box maxH="calc(100vh - 240px)" overflow="auto">
            <Table.Root size="sm" variant="line" stickyHeader>
              <Table.Header bg="surface.subtle">
                <Table.Row>
                  <SortableHeader
                    label="State"
                    sortKey="state"
                    current={sortKey}
                    onSort={setSortKey}
                  />
                  <SortableHeader
                    label="Name"
                    sortKey="name"
                    current={sortKey}
                    onSort={setSortKey}
                  />
                  <SortableHeader
                    label="Image"
                    sortKey="image"
                    current={sortKey}
                    onSort={setSortKey}
                  />
                  <SortableHeader
                    label="CPU"
                    sortKey="cpu"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <SortableHeader
                    label="Memory"
                    sortKey="memory"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <SortableHeader
                    label="↓ Net"
                    sortKey="netRecv"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <SortableHeader
                    label="↑ Net"
                    sortKey="netSent"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                  <SortableHeader
                    label="Started"
                    sortKey="started"
                    current={sortKey}
                    onSort={setSortKey}
                    textAlign="end"
                  />
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {items.map((c) => (
                  <Table.Row
                    key={c.id}
                    cursor="pointer"
                    onClick={() => setSelectedId(c.id)}
                    bg={c.id === selectedId ? "surface.subtle" : undefined}
                    _hover={{ bg: "surface.subtle" }}
                    transition="background 100ms ease"
                  >
                    <Table.Cell>
                      <StatusBadge
                        status={stateToStatus(c.state)}
                        label={c.state}
                      />
                    </Table.Cell>
                    <Table.Cell>
                      <VStack align="start" gap="0">
                        <Text fontSize="sm" fontWeight="medium">
                          {c.name}
                        </Text>
                        <Text fontSize="xs" color="fg.subtle" fontFamily="mono">
                          {c.id.slice(0, 12)}
                        </Text>
                      </VStack>
                    </Table.Cell>
                    <Table.Cell>
                      <Text fontSize="sm" fontFamily="mono" color="fg.muted">
                        {c.image}
                      </Text>
                    </Table.Cell>
                    <Table.Cell
                      textAlign="end"
                      fontFamily="mono"
                      fontVariantNumeric="tabular-nums"
                    >
                      {c.cpuUsedPerc.toFixed(1)}%
                    </Table.Cell>
                    <Table.Cell
                      textAlign="end"
                      fontFamily="mono"
                      fontVariantNumeric="tabular-nums"
                    >
                      {c.memUsedPerc.toFixed(1)}%
                    </Table.Cell>
                    <Table.Cell
                      textAlign="end"
                      fontFamily="mono"
                      fontVariantNumeric="tabular-nums"
                    >
                      {formatBytes(c.netBitsRecv / 8)}/s
                    </Table.Cell>
                    <Table.Cell
                      textAlign="end"
                      fontFamily="mono"
                      fontVariantNumeric="tabular-nums"
                    >
                      {formatBytes(c.netBitsSent / 8)}/s
                    </Table.Cell>
                    <Table.Cell textAlign="end" fontSize="xs" color="fg.muted">
                      {relativeTime(c.startedAt)}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
          </Box>
        )}
      </Box>

      <ContainerDrawer
        container={selectedContainer}
        requestedId={selectedId}
        onClose={() => setSelectedId(null)}
      />
    </VStack>
  );
}
