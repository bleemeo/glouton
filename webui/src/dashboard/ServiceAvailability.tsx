import { Box, chakra, HStack, Text, VStack } from "@chakra-ui/react";
import { useMemo, useState } from "react";

import { usePromQLRange, useStoreInfo } from "../api/hooks";
import { samplesOf, type Sample } from "./promql";
import { DEFAULT_RANGE_ID, isRangeAvailable, RANGES, type Range } from "./ranges";

const STATUS_COLOR: Record<number, string> = {
  0: "#10B981", // ok
  1: "#F59E0B", // warn
  2: "#EF4444", // crit
  3: "#9CA3AF", // unknown
};

const STATUS_LABEL: Record<number, string> = {
  0: "OK",
  1: "Warning",
  2: "Critical",
  3: "Unknown",
};

function statusColor(code: number): string {
  if (code === 0) return STATUS_COLOR[0];
  if (code === 1) return STATUS_COLOR[1];
  if (code >= 2 && code < 3) return STATUS_COLOR[2];
  return STATUS_COLOR[3];
}

function statusLabel(code: number): string {
  if (code === 0) return STATUS_LABEL[0];
  if (code === 1) return STATUS_LABEL[1];
  if (code >= 2 && code < 3) return STATUS_LABEL[2];
  return STATUS_LABEL[3];
}

type Segment = {
  from: number; // unix seconds
  to: number;
  status: number;
  durationSec: number;
};

/**
 * compressToSegments collapses a sample stream into a list of segments
 * by merging consecutive samples whose status code is the same. Each
 * segment spans from one timestamp to the next; the last segment runs
 * up to `endSec` so a stale series still renders edge-to-edge.
 */
function compressToSegments(samples: Sample[], endSec: number): Segment[] {
  if (samples.length === 0) return [];

  const segments: Segment[] = [];
  let from = samples[0].t;
  let status = samples[0].v;

  for (let i = 1; i < samples.length; i++) {
    const next = samples[i];
    if (next.v !== status) {
      segments.push({ from, to: next.t, status, durationSec: next.t - from });
      from = next.t;
      status = next.v;
    }
  }

  segments.push({ from, to: endSec, status, durationSec: Math.max(0, endSec - from) });

  return segments;
}

function formatDuration(sec: number): string {
  if (sec < 60) return `${Math.round(sec)}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)}h`;
  return `${(sec / 86400).toFixed(1)}d`;
}

function formatTime(unixSec: number): string {
  return new Date(unixSec * 1000).toLocaleTimeString(undefined, {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

const RECENT_CHANGES = 5;

type Props = {
  serviceName: string;
};

export function ServiceAvailability({ serviceName }: Props) {
  const storeInfo = useStoreInfo();
  const [range, setRange] = useState<Range>(
    () =>
      RANGES.find((r) => r.id === DEFAULT_RANGE_ID) ??
      RANGES.find((r) => r.id === "1h") ??
      RANGES[0],
  );

  // PromQL escape: service names should never contain " or \, but
  // play it safe.
  const escapedName = serviceName.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  const query = `service_status{service="${escapedName}"}`;
  const res = usePromQLRange(query, range.seconds, range.step);

  // Take the first series. If a host has multiple instances of the same
  // service (e.g. two redis containers), v1 keeps it simple and shows
  // the first one returned.
  const series = res.data?.data?.result?.[0];
  const samples = useMemo(() => samplesOf(series), [series]);

  const endSec = Math.floor(Date.now() / 1000);
  const startSec = endSec - range.seconds;
  const segments = useMemo(() => compressToSegments(samples, endSec), [samples, endSec]);

  // Stats: uptime %, per-status distribution, change count, current
  // state duration, and the longest non-OK streak.
  const stats = useMemo(() => {
    if (segments.length === 0) {
      return {
        uptimePct: null as number | null,
        distribution: null as Record<number, number> | null,
        changes: 0,
        currentStatus: null as number | null,
        currentDurationSec: 0,
        longestOutage: null as { status: number; durationSec: number; from: number } | null,
      };
    }

    const totalSec = segments.reduce((acc, s) => acc + s.durationSec, 0) || 1;
    const last = segments[segments.length - 1];

    const buckets: Record<number, number> = { 0: 0, 1: 0, 2: 0, 3: 0 };
    let longestOutage: { status: number; durationSec: number; from: number } | null = null;

    for (const seg of segments) {
      const bucketKey = seg.status === 0 ? 0 : seg.status === 1 ? 1 : seg.status >= 2 && seg.status < 3 ? 2 : 3;
      buckets[bucketKey] = (buckets[bucketKey] ?? 0) + seg.durationSec;

      if (seg.status !== 0 && (!longestOutage || seg.durationSec > longestOutage.durationSec)) {
        longestOutage = { status: bucketKey, durationSec: seg.durationSec, from: seg.from };
      }
    }

    const distribution: Record<number, number> = {};
    for (const k of Object.keys(buckets)) {
      const key = Number(k);
      distribution[key] = (buckets[key] / totalSec) * 100;
    }

    return {
      uptimePct: distribution[0],
      distribution,
      changes: Math.max(0, segments.length - 1),
      currentStatus: last.status,
      currentDurationSec: last.durationSec,
      longestOutage,
    };
  }, [segments]);

  // Last check timestamp from the freshest sample (not the segment
  // end, which we extend to "now" for the stripe).
  const lastCheckSec = samples.length > 0 ? samples[samples.length - 1].t : null;

  // Recent transitions: take the boundary points (from segment i to
  // segment i+1) and list the most recent first.
  const transitions = useMemo(() => {
    const out: { at: number; from: number; to: number }[] = [];
    for (let i = 1; i < segments.length; i++) {
      out.push({ at: segments[i].from, from: segments[i - 1].status, to: segments[i].status });
    }
    return out.reverse().slice(0, RECENT_CHANGES);
  }, [segments]);

  return (
    <VStack align="stretch" gap="3">
      <HStack justify="space-between" gap="2" wrap="wrap">
        <Text
          fontSize="xs"
          color="fg.muted"
          textTransform="uppercase"
          letterSpacing="0.06em"
        >
          Availability
        </Text>
        <RangePicker
          value={range.id}
          onChange={setRange}
          oldestPointMs={storeInfo.data?.oldest_point_ms}
        />
      </HStack>

      <Stats {...stats} />

      <StatusStripe
        segments={segments}
        startSec={startSec}
        endSec={endSec}
        lastCheckSec={lastCheckSec}
        loading={res.loading}
      />

      {transitions.length > 0 ? <TransitionsList transitions={transitions} /> : null}
    </VStack>
  );
}

function Stats({
  uptimePct,
  distribution,
  changes,
  currentStatus,
  currentDurationSec,
  longestOutage,
}: {
  uptimePct: number | null;
  distribution: Record<number, number> | null;
  changes: number;
  currentStatus: number | null;
  currentDurationSec: number;
  longestOutage: { status: number; durationSec: number; from: number } | null;
}) {
  const ok = uptimePct != null;
  // Only the buckets with a non-zero share, to keep the line compact.
  const distributionEntries = distribution
    ? ([0, 1, 2, 3] as const)
        .map((k) => [k, distribution[k] ?? 0] as const)
        .filter(([, pct]) => pct > 0)
    : [];

  return (
    <VStack align="stretch" gap="1.5">
      <HStack gap="4" wrap="wrap" fontSize="sm">
        <HStack gap="1.5">
          <Text color="fg.muted" fontSize="xs" textTransform="uppercase" letterSpacing="0.04em">
            Uptime
          </Text>
          <Text
            fontFamily="mono"
            fontVariantNumeric="tabular-nums"
            fontWeight="semibold"
            color={ok && uptimePct >= 99 ? "#10B981" : ok && uptimePct >= 95 ? "#F59E0B" : "fg.default"}
          >
            {ok ? `${uptimePct.toFixed(1)}%` : "—"}
          </Text>
        </HStack>
        <HStack gap="1.5">
          <Text color="fg.muted" fontSize="xs" textTransform="uppercase" letterSpacing="0.04em">
            Changes
          </Text>
          <Text fontFamily="mono" fontVariantNumeric="tabular-nums" fontWeight="semibold">
            {changes}
          </Text>
        </HStack>
        {currentStatus != null ? (
          <HStack gap="1.5">
            <Text color="fg.muted" fontSize="xs" textTransform="uppercase" letterSpacing="0.04em">
              Current
            </Text>
            <Box w="2" h="2" borderRadius="full" bg={statusColor(currentStatus)} />
            <Text fontFamily="mono" fontVariantNumeric="tabular-nums" fontWeight="semibold">
              {statusLabel(currentStatus)} for {formatDuration(currentDurationSec)}
            </Text>
          </HStack>
        ) : null}
      </HStack>

      {distributionEntries.length > 1 || longestOutage ? (
        <HStack gap="4" wrap="wrap" fontSize="xs" color="fg.subtle" fontFamily="mono">
          {distributionEntries.length > 1 ? (
            <HStack gap="2">
              {distributionEntries.map(([k, pct]) => (
                <HStack key={k} gap="1">
                  <Box w="1.5" h="1.5" borderRadius="full" bg={statusColor(k)} />
                  <Text>
                    {statusLabel(k)} {pct.toFixed(1)}%
                  </Text>
                </HStack>
              ))}
            </HStack>
          ) : null}
          {longestOutage ? (
            <Text>
              Longest {statusLabel(longestOutage.status).toLowerCase()} streak:{" "}
              {formatDuration(longestOutage.durationSec)}
            </Text>
          ) : null}
        </HStack>
      ) : null}
    </VStack>
  );
}

function StatusStripe({
  segments,
  startSec,
  endSec,
  lastCheckSec,
  loading,
}: {
  segments: Segment[];
  startSec: number;
  endSec: number;
  lastCheckSec: number | null;
  loading: boolean;
}) {
  if (segments.length === 0) {
    return (
      <Box
        bg="surface.subtle"
        borderWidth="1px"
        borderColor="border.subtle"
        borderRadius="md"
        h="32px"
        display="flex"
        alignItems="center"
        justifyContent="center"
      >
        <Text fontSize="xs" color="fg.subtle">
          {loading ? "Loading…" : "No history yet for this range."}
        </Text>
      </Box>
    );
  }

  return (
    <VStack align="stretch" gap="1">
      <HStack
        gap="0"
        align="stretch"
        h="24px"
        borderRadius="md"
        overflow="hidden"
        borderWidth="1px"
        borderColor="border.subtle"
      >
        {segments.map((seg, i) => (
          <Box
            key={`${seg.from}-${i}`}
            bg={statusColor(seg.status)}
            flexGrow={Math.max(1, seg.durationSec)}
            flexShrink={1}
            flexBasis="0"
            title={`${statusLabel(seg.status)} from ${formatTime(seg.from)} to ${formatTime(seg.to)} (${formatDuration(seg.durationSec)})`}
            _hover={{ opacity: 0.8 }}
            transition="opacity 100ms ease"
          />
        ))}
      </HStack>
      <HStack justify="space-between" px="0.5" fontSize="2xs" color="fg.subtle" fontFamily="mono">
        <Text>{formatTime(startSec)}</Text>
        {lastCheckSec ? (
          <Text title={`Last sample at ${formatTime(lastCheckSec)}`}>
            last check {formatAgo(endSec - lastCheckSec)}
          </Text>
        ) : null}
        <Text>{formatTime(endSec)}</Text>
      </HStack>
    </VStack>
  );
}

function formatAgo(seconds: number): string {
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${Math.floor(seconds)}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function TransitionsList({
  transitions,
}: {
  transitions: { at: number; from: number; to: number }[];
}) {
  return (
    <VStack align="stretch" gap="1">
      <Text
        fontSize="2xs"
        color="fg.subtle"
        textTransform="uppercase"
        letterSpacing="0.06em"
        mt="1"
      >
        Recent changes
      </Text>
      {transitions.map((t, i) => (
        <HStack
          key={`${t.at}-${i}`}
          gap="2"
          fontSize="xs"
          fontFamily="mono"
          fontVariantNumeric="tabular-nums"
        >
          <Text color="fg.muted" minW="8ch">
            {formatTime(t.at)}
          </Text>
          <Box w="2" h="2" borderRadius="full" bg={statusColor(t.from)} />
          <Text color="fg.subtle">{statusLabel(t.from)}</Text>
          <Text color="fg.subtle">→</Text>
          <Box w="2" h="2" borderRadius="full" bg={statusColor(t.to)} />
          <Text color="fg.default" fontWeight="medium">
            {statusLabel(t.to)}
          </Text>
        </HStack>
      ))}
    </VStack>
  );
}

function RangePicker({
  value,
  onChange,
  oldestPointMs,
}: {
  value: string;
  onChange: (r: Range) => void;
  oldestPointMs: number | undefined;
}) {
  return (
    <HStack gap="0.5">
      {RANGES.map((r) => {
        const active = r.id === value;
        const available = isRangeAvailable(r, oldestPointMs);

        return (
          <chakra.button
            key={r.id}
            type="button"
            onClick={() => available && onChange(r)}
            disabled={!available}
            px="1.5"
            py="0.5"
            fontSize="2xs"
            fontFamily="mono"
            fontWeight={active ? "semibold" : "medium"}
            color={active ? "fg.default" : available ? "fg.muted" : "fg.subtle"}
            bg={active ? "surface.subtle" : "transparent"}
            borderRadius="sm"
            cursor={available ? "pointer" : "not-allowed"}
            opacity={available ? 1 : 0.4}
            _hover={available ? { bg: "surface.subtle" } : {}}
            title={available ? `Last ${r.label}` : `Not enough history yet`}
          >
            {r.label}
          </chakra.button>
        );
      })}
    </HStack>
  );
}

