import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useMemo } from "react";

import { usePromQLRange } from "../api/hooks";
import { samplesOf, type Sample } from "../dashboard/promql";
import type { Range } from "../dashboard/ranges";
import { instanceSelector } from "./promql";

const OK_COLOR = "#10B981";
const DOWN_COLOR = "#EF4444";

type Segment = {
  from: number;
  to: number;
  up: 0 | 1;
  durationSec: number;
};

function compressToSegments(samples: Sample[], endSec: number): Segment[] {
  if (samples.length === 0) return [];

  const segments: Segment[] = [];
  let from = samples[0].t;
  let up: 0 | 1 = samples[0].v >= 1 ? 1 : 0;

  for (let i = 1; i < samples.length; i++) {
    const next = samples[i];
    const nextUp: 0 | 1 = next.v >= 1 ? 1 : 0;
    if (nextUp !== up) {
      segments.push({ from, to: next.t, up, durationSec: next.t - from });
      from = next.t;
      up = nextUp;
    }
  }

  segments.push({
    from,
    to: endSec,
    up,
    durationSec: Math.max(0, endSec - from),
  });
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

function formatAgo(seconds: number): string {
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${Math.floor(seconds)}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

const RECENT_CHANGES = 5;

type Props = {
  // The monitor's `name` (matches the `instance` label on probe_*
  // metrics emitted by the blackbox manager). NOT the URL — see
  // promql.ts/instanceSelector for the rationale.
  name: string;
  range: Range;
};

/**
 * MonitorAvailability is the probe_success analogue of
 * dashboard/ServiceAvailability — same idea (status stripe, uptime %,
 * recent transitions), but the underlying metric only has two states
 * (probe_success = 0 or 1) so we render in green/red without the warn
 * tier.
 */
export function MonitorAvailability({ name, range }: Props) {
  const query = `probe_success${instanceSelector(name)}`;
  const res = usePromQLRange(query, range.seconds, range.step);

  const series = res.data?.data?.result?.[0];
  const samples = useMemo(() => samplesOf(series), [series]);

  const endSec = Math.floor(Date.now() / 1000);
  const startSec = endSec - range.seconds;
  const segments = useMemo(
    () => compressToSegments(samples, endSec),
    [samples, endSec],
  );

  const stats = useMemo(() => {
    if (segments.length === 0) {
      return {
        uptimePct: null as number | null,
        changes: 0,
        currentUp: null as 0 | 1 | null,
        currentDurationSec: 0,
        longestOutage: null as { durationSec: number; from: number } | null,
      };
    }

    const totalSec = segments.reduce((acc, s) => acc + s.durationSec, 0) || 1;
    const okSec = segments
      .filter((s) => s.up === 1)
      .reduce((acc, s) => acc + s.durationSec, 0);
    const last = segments[segments.length - 1];

    let longestOutage: { durationSec: number; from: number } | null = null;
    for (const seg of segments) {
      if (
        seg.up === 0 &&
        (!longestOutage || seg.durationSec > longestOutage.durationSec)
      ) {
        longestOutage = { durationSec: seg.durationSec, from: seg.from };
      }
    }

    return {
      uptimePct: (okSec / totalSec) * 100,
      changes: Math.max(0, segments.length - 1),
      currentUp: last.up,
      currentDurationSec: last.durationSec,
      longestOutage,
    };
  }, [segments]);

  const lastCheckSec =
    samples.length > 0 ? samples[samples.length - 1].t : null;

  const transitions = useMemo(() => {
    const out: { at: number; from: 0 | 1; to: 0 | 1 }[] = [];
    for (let i = 1; i < segments.length; i++) {
      out.push({
        at: segments[i].from,
        from: segments[i - 1].up,
        to: segments[i].up,
      });
    }
    return out.reverse().slice(0, RECENT_CHANGES);
  }, [segments]);

  return (
    <VStack align="stretch" gap="3">
      <HStack gap="4" wrap="wrap" fontSize="sm">
        <HStack gap="1.5">
          <Text
            color="fg.muted"
            fontSize="xs"
            textTransform="uppercase"
            letterSpacing="0.04em"
          >
            Uptime
          </Text>
          <Text
            fontFamily="mono"
            fontVariantNumeric="tabular-nums"
            fontWeight="semibold"
            color={
              stats.uptimePct == null
                ? "fg.default"
                : stats.uptimePct >= 99
                  ? OK_COLOR
                  : stats.uptimePct >= 95
                    ? "#F59E0B"
                    : DOWN_COLOR
            }
          >
            {stats.uptimePct != null ? `${stats.uptimePct.toFixed(2)}%` : "—"}
          </Text>
        </HStack>

        <HStack gap="1.5">
          <Text
            color="fg.muted"
            fontSize="xs"
            textTransform="uppercase"
            letterSpacing="0.04em"
          >
            Outages
          </Text>
          <Text
            fontFamily="mono"
            fontVariantNumeric="tabular-nums"
            fontWeight="semibold"
          >
            {Math.floor(stats.changes / 2)}
          </Text>
        </HStack>

        {stats.currentUp != null ? (
          <HStack gap="1.5">
            <Text
              color="fg.muted"
              fontSize="xs"
              textTransform="uppercase"
              letterSpacing="0.04em"
            >
              Current
            </Text>
            <Box
              w="2"
              h="2"
              borderRadius="full"
              bg={stats.currentUp === 1 ? OK_COLOR : DOWN_COLOR}
            />
            <Text
              fontFamily="mono"
              fontVariantNumeric="tabular-nums"
              fontWeight="semibold"
            >
              {stats.currentUp === 1 ? "Up" : "Down"} for{" "}
              {formatDuration(stats.currentDurationSec)}
            </Text>
          </HStack>
        ) : null}

        {stats.longestOutage ? (
          <HStack gap="1.5">
            <Text
              color="fg.muted"
              fontSize="xs"
              textTransform="uppercase"
              letterSpacing="0.04em"
            >
              Longest down
            </Text>
            <Text
              fontFamily="mono"
              fontVariantNumeric="tabular-nums"
              fontWeight="semibold"
            >
              {formatDuration(stats.longestOutage.durationSec)}
            </Text>
          </HStack>
        ) : null}
      </HStack>

      <StatusStripe
        segments={segments}
        startSec={startSec}
        endSec={endSec}
        lastCheckSec={lastCheckSec}
        loading={res.loading}
      />

      {transitions.length > 0 ? (
        <TransitionsList transitions={transitions} />
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
            bg={seg.up === 1 ? OK_COLOR : DOWN_COLOR}
            flexGrow={Math.max(1, seg.durationSec)}
            flexShrink={1}
            flexBasis="0"
            title={`${seg.up === 1 ? "Up" : "Down"} from ${formatTime(seg.from)} to ${formatTime(seg.to)} (${formatDuration(seg.durationSec)})`}
            _hover={{ opacity: 0.8 }}
            transition="opacity 100ms ease"
          />
        ))}
      </HStack>
      <HStack
        justify="space-between"
        px="0.5"
        fontSize="2xs"
        color="fg.subtle"
        fontFamily="mono"
      >
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

function TransitionsList({
  transitions,
}: {
  transitions: { at: number; from: 0 | 1; to: 0 | 1 }[];
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
          <Box
            w="2"
            h="2"
            borderRadius="full"
            bg={t.from === 1 ? OK_COLOR : DOWN_COLOR}
          />
          <Text color="fg.subtle">{t.from === 1 ? "Up" : "Down"}</Text>
          <Text color="fg.subtle">→</Text>
          <Box
            w="2"
            h="2"
            borderRadius="full"
            bg={t.to === 1 ? OK_COLOR : DOWN_COLOR}
          />
          <Text color="fg.default" fontWeight="medium">
            {t.to === 1 ? "Up" : "Down"}
          </Text>
        </HStack>
      ))}
    </VStack>
  );
}
