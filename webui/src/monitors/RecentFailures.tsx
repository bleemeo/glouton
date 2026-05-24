import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useMemo } from "react";

import { usePromQLRange } from "../api/hooks";
import { samplesOf, type Sample } from "../dashboard/promql";
import type { Range } from "../dashboard/ranges";
import { instanceSelector } from "./promql";

// We list at most this many incidents — past the worst few the
// stripe is the better summary.
const MAX_FAILURES = 8;

type Failure = {
  fromSec: number;
  toSec: number | null; // null = still ongoing
  durationSec: number;
  reason: string | null;
};

type Props = {
  name: string;
  range: Range;
};

/**
 * RecentFailures walks the probe_success matrix to extract the
 * contiguous down periods, then samples the companion metrics (HTTP
 * status code, TLS error flag, DNS rcode) at the start of each period
 * to guess the failure mode. Cheaper than running 4 queries per
 * incident: we do 4 range queries total and join client-side.
 */
export function RecentFailures({ name, range }: Props) {
  const sel = instanceSelector(name);
  const probe = usePromQLRange(
    `probe_success${sel}`,
    range.seconds,
    range.step,
  );
  const httpStatus = usePromQLRange(
    `probe_http_status_code${sel}`,
    range.seconds,
    range.step,
  );
  const tlsError = usePromQLRange(
    `probe_failed_due_to_tls_error${sel}`,
    range.seconds,
    range.step,
  );
  const dnsRcode = usePromQLRange(
    `probe_dns_rcode${sel}`,
    range.seconds,
    range.step,
  );

  const { failures, sampleCount } = useMemo(() => {
    const samples = samplesOf(probe.data?.data?.result?.[0]);
    if (samples.length === 0) {
      return { failures: [] as Failure[], sampleCount: 0 };
    }

    const httpSamples = samplesOf(httpStatus.data?.data?.result?.[0]);
    const tlsSamples = samplesOf(tlsError.data?.data?.result?.[0]);
    const dnsSamples = samplesOf(dnsRcode.data?.data?.result?.[0]);
    const nowSec = Math.floor(Date.now() / 1000);

    const result: Failure[] = [];
    let downStart: number | null = null;

    for (let i = 0; i < samples.length; i++) {
      const s = samples[i];
      const isDown = s.v < 1;

      if (isDown && downStart == null) {
        downStart = s.t;
      } else if (!isDown && downStart != null) {
        result.push({
          fromSec: downStart,
          toSec: s.t,
          durationSec: s.t - downStart,
          reason: reasonAt(downStart, httpSamples, tlsSamples, dnsSamples),
        });
        downStart = null;
      }
    }

    // Tail: still down at the end of the window → ongoing incident.
    if (downStart != null) {
      result.push({
        fromSec: downStart,
        toSec: null,
        durationSec: nowSec - downStart,
        reason: reasonAt(downStart, httpSamples, tlsSamples, dnsSamples),
      });
    }

    return {
      failures: result.reverse().slice(0, MAX_FAILURES),
      sampleCount: samples.length,
    };
  }, [probe.data, httpStatus.data, tlsError.data, dnsRcode.data]);

  return (
    <VStack align="stretch" gap="2">
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        Recent failures
      </Text>
      {failures.length === 0 ? (
        <Box
          bg="surface.subtle"
          borderWidth="1px"
          borderColor="border.subtle"
          borderRadius="md"
          p="3"
        >
          <Text fontSize="xs" color="fg.subtle">
            {emptyMessage(probe.loading, sampleCount)}
          </Text>
        </Box>
      ) : (
        <VStack
          align="stretch"
          gap="0"
          bg="surface.subtle"
          borderWidth="1px"
          borderColor="border.subtle"
          borderRadius="md"
          divideY="1px"
          divideColor="border.subtle"
        >
          {failures.map((f, i) => (
            <FailureRow key={`${f.fromSec}-${i}`} failure={f} />
          ))}
        </VStack>
      )}
    </VStack>
  );
}

// emptyMessage distinguishes the three "no failures" cases so a fresh
// monitor doesn't look like "we already proved it's up", and a brand
// new agent doesn't look like we silently lost the history.
function emptyMessage(loading: boolean, sampleCount: number): string {
  if (loading && sampleCount === 0) return "Loading…";
  if (sampleCount === 0) {
    return "No data yet — waiting for the first probe to complete.";
  }
  return "No failures over the selected range.";
}

function FailureRow({ failure }: { failure: Failure }) {
  const ongoing = failure.toSec == null;

  return (
    <HStack
      gap="3"
      px="3"
      py="2"
      fontSize="xs"
      fontFamily="mono"
      fontVariantNumeric="tabular-nums"
    >
      <Text color={ongoing ? "#EF4444" : "fg.muted"} minW="14ch">
        {formatTime(failure.fromSec)}
      </Text>
      <Text color="fg.default" fontWeight="medium" minW="8ch">
        {formatDuration(failure.durationSec)}
      </Text>
      <Text color="fg.muted" flex="1" truncate>
        {failure.reason ?? "—"}
      </Text>
      {ongoing ? (
        <Text color="#EF4444" fontWeight="semibold">
          ongoing
        </Text>
      ) : null}
    </HStack>
  );
}

function reasonAt(
  tSec: number,
  httpSamples: Sample[],
  tlsSamples: Sample[],
  dnsSamples: Sample[],
): string | null {
  const tls = closestAt(tlsSamples, tSec);
  if (tls != null && tls >= 1) return "TLS error";

  const dns = closestAt(dnsSamples, tSec);
  if (dns != null && dns > 0) return `DNS rcode ${Math.round(dns)}`;

  const code = closestAt(httpSamples, tSec);
  if (code != null && code > 0) return `HTTP ${Math.round(code)}`;

  return "timeout / unreachable";
}

// closestAt returns the value of the sample whose timestamp is nearest
// to tSec, bounded to a small window so we don't pick a stale value
// from minutes away.
function closestAt(samples: Sample[], tSec: number): number | null {
  const WINDOW_SEC = 300;
  let best: Sample | null = null;
  let bestDist = Infinity;

  for (const s of samples) {
    const dist = Math.abs(s.t - tSec);
    if (dist < bestDist && dist <= WINDOW_SEC) {
      bestDist = dist;
      best = s;
    }
  }

  return best?.v ?? null;
}

function formatTime(unixSec: number): string {
  return new Date(unixSec * 1000).toLocaleString(undefined, {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatDuration(sec: number): string {
  if (sec < 60) return `${Math.round(sec)}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)}h`;
  return `${(sec / 86400).toFixed(1)}d`;
}
