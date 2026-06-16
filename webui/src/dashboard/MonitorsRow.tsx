import { Box, chakra, HStack, Text, VStack } from "@chakra-ui/react";
import { useMemo } from "react";
import { useNavigate } from "react-router-dom";

import { useFetch, usePromQLRange } from "../api/hooks";
import type { Monitor, MonitorsResponse } from "../api/types";
import { samplesOf, seriesByLabel } from "./promql";

const OK_COLOR = "#10B981";
const DOWN_COLOR = "#EF4444";
const UNKNOWN_COLOR = "#9CA3AF";

// Pull the latest probe_success for every monitor in a single PromQL
// call. A small window keeps the response cheap; we only care about
// the freshest sample.
const PROBE_RANGE_SECONDS = 5 * 60;
const PROBE_STEP_SECONDS = 30;

type MonitorState = {
  monitor: Monitor;
  status: "ok" | "down" | "unknown";
};

export function MonitorsRow() {
  const res = useFetch<MonitorsResponse>("/data/monitors", 60_000);
  const monitors = useMemo(() => res.data?.monitors ?? [], [res.data]);

  // Skip the PromQL query entirely when no monitors are configured —
  // the row is hidden in that case anyway.
  const probe = usePromQLRange(
    monitors.length > 0 ? "probe_success" : null,
    PROBE_RANGE_SECONDS,
    PROBE_STEP_SECONDS,
  );

  const byInstance = useMemo(
    () => seriesByLabel(probe.data, "instance"),
    [probe.data],
  );

  const states: MonitorState[] = useMemo(() => {
    return monitors.map((m) => {
      const samples = samplesOf(byInstance[m.name]);
      const v = samples.length > 0 ? samples[samples.length - 1].v : null;
      let status: MonitorState["status"] = "unknown";
      if (v != null) status = v >= 1 ? "ok" : "down";
      return { monitor: m, status };
    });
  }, [monitors, byInstance]);

  const navigate = useNavigate();

  if (!res.loading && monitors.length === 0) {
    return null;
  }

  // Worst-first ordering so anything down jumps to the front.
  const sorted = [...states].sort((a, b) => {
    const rank = { down: 2, unknown: 1, ok: 0 } as const;
    if (rank[b.status] !== rank[a.status])
      return rank[b.status] - rank[a.status];
    return a.monitor.name.localeCompare(b.monitor.name);
  });

  return (
    <VStack align="stretch" gap="2">
      <Text
        fontSize="xs"
        fontWeight="semibold"
        color="fg.muted"
        letterSpacing="0.08em"
        textTransform="uppercase"
      >
        Monitors
      </Text>
      <HStack gap="1.5" wrap="wrap">
        {sorted.map(({ monitor, status }) => (
          <MonitorChip
            key={monitor.url}
            monitor={monitor}
            status={status}
            onClick={() =>
              navigate(`/monitors/${encodeURIComponent(monitor.name)}`)
            }
          />
        ))}
      </HStack>
    </VStack>
  );
}

function statusColor(status: MonitorState["status"]): string {
  if (status === "ok") return OK_COLOR;
  if (status === "down") return DOWN_COLOR;
  return UNKNOWN_COLOR;
}

function MonitorChip({
  monitor,
  status,
  onClick,
}: {
  monitor: Monitor;
  status: MonitorState["status"];
  onClick: () => void;
}) {
  const color = statusColor(status);
  const detail = `${monitor.name} — ${monitor.url} (${status})`;

  return (
    <chakra.button
      type="button"
      onClick={onClick}
      px="2.5"
      py="1"
      bg="surface.panel"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="full"
      cursor="pointer"
      transition="all 100ms ease"
      _hover={{ bg: "surface.subtle", borderColor: "border.default" }}
      title={detail}
      display="inline-flex"
      alignItems="center"
      gap="2"
    >
      <Box w="2" h="2" borderRadius="full" bg={color} />
      <Text fontSize="xs" fontFamily="mono" color="fg.default">
        {monitor.name}
      </Text>
    </chakra.button>
  );
}
