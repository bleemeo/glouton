import { Box, chakra, HStack, Text, VStack } from "@chakra-ui/react";

import { StatusBadge } from "../app/StatusBadge";
import { Sparkline } from "../dashboard/Sparkline";
import { formatCertExpiry, formatLatency } from "./format";
import type { MonitorState } from "./useMonitorStates";

type Props = {
  state: MonitorState;
  loading: boolean;
  onClick: () => void;
};

export function MonitorCard({ state, loading, onClick }: Props) {
  const { monitor, status, probeSamples, uptimePct, p95Sec } = state;

  const badgeLabel =
    status === "ok" ? "Up" : status === "down" ? "Down" : "Unknown";
  const badgeStatus =
    status === "ok" ? "ok" : status === "down" ? "crit" : "unknown";

  const failureReason = computeFailureReason(state);
  const certInfo = formatCertExpiry(state.certExpiryUnix);
  const lastChecked = formatLastChecked(state.lastCheckSec);

  return (
    <chakra.button
      type="button"
      onClick={onClick}
      textAlign="start"
      bg="surface.panel"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="lg"
      p="4"
      cursor="pointer"
      transition="border-color 120ms ease, background 120ms ease"
      _hover={{ borderColor: "border.default", bg: "surface.subtle" }}
    >
      <VStack align="stretch" gap="3">
        <HStack justify="space-between" align="start" gap="2">
          <VStack align="start" gap="0.5" minW="0" flex="1">
            <Text fontWeight="semibold" fontSize="sm" truncate>
              {monitor.name}
            </Text>
            <Text
              fontSize="xs"
              color="fg.muted"
              fontFamily="mono"
              truncate
              maxW="100%"
            >
              {monitor.url}
            </Text>
          </VStack>
          <VStack align="end" gap="0.5">
            <StatusBadge status={badgeStatus} label={badgeLabel} />
            {failureReason ? (
              <Text
                fontSize="2xs"
                fontFamily="mono"
                color="#EF4444"
                whiteSpace="nowrap"
              >
                {failureReason}
              </Text>
            ) : null}
          </VStack>
        </HStack>

        <Box>
          {probeSamples.length > 0 ? (
            <Sparkline
              data={probeSamples}
              color={status === "down" ? "#EF4444" : "#10B981"}
              yMax={1}
              height={36}
            />
          ) : (
            <Box h="36px" display="flex" alignItems="center">
              <Text fontSize="xs" color="fg.subtle">
                {loading ? "Loading…" : "No history yet"}
              </Text>
            </Box>
          )}
        </Box>

        <HStack gap="4" wrap="wrap" fontSize="xs" fontFamily="mono">
          <VStack align="start" gap="0">
            <Text
              color="fg.subtle"
              fontSize="2xs"
              textTransform="uppercase"
              letterSpacing="0.06em"
            >
              Uptime 24h
            </Text>
            <Text
              fontWeight="semibold"
              color={
                uptimePct == null
                  ? "fg.muted"
                  : uptimePct >= 99
                    ? "#10B981"
                    : uptimePct >= 95
                      ? "#F59E0B"
                      : "#EF4444"
              }
            >
              {uptimePct != null ? `${uptimePct.toFixed(1)}%` : "—"}
            </Text>
          </VStack>

          <VStack align="start" gap="0">
            <Text
              color="fg.subtle"
              fontSize="2xs"
              textTransform="uppercase"
              letterSpacing="0.06em"
            >
              p95 24h
            </Text>
            <Text fontWeight="semibold">{formatLatency(p95Sec)}</Text>
          </VStack>

          {monitor.scheme === "https" ? (
            <VStack align="start" gap="0">
              <Text
                color="fg.subtle"
                fontSize="2xs"
                textTransform="uppercase"
                letterSpacing="0.06em"
              >
                Certificate
              </Text>
              <Text
                fontWeight="semibold"
                color={
                  certInfo.level === "crit"
                    ? "#EF4444"
                    : certInfo.level === "warn"
                      ? "#F59E0B"
                      : "fg.default"
                }
              >
                {certInfo.label}
              </Text>
            </VStack>
          ) : null}

          <VStack align="start" gap="0" ms="auto">
            <Text
              color="fg.subtle"
              fontSize="2xs"
              textTransform="uppercase"
              letterSpacing="0.06em"
            >
              Module
            </Text>
            <HStack gap="1.5">
              <Text color="fg.muted">{monitor.module}</Text>
              {monitor.source === "bleemeo" ? (
                <Box
                  px="1.5"
                  py="0.5"
                  fontSize="2xs"
                  fontWeight="semibold"
                  borderRadius="sm"
                  bg="rgba(79, 141, 245, 0.12)"
                  color="#4F8DF5"
                  letterSpacing="0.04em"
                  textTransform="uppercase"
                  title="Provisioned by Bleemeo Cloud"
                >
                  Bleemeo
                </Box>
              ) : null}
            </HStack>
          </VStack>
        </HStack>

        {lastChecked ? (
          <Text fontSize="2xs" color="fg.subtle" fontFamily="mono">
            Last check {lastChecked}
          </Text>
        ) : null}
      </VStack>
    </chakra.button>
  );
}

// computeFailureReason produces the small red tag shown under the
// status badge when the monitor is down. Specific signal first
// (TLS > DNS > HTTP code) — and HTTP 2xx/3xx with probe_success==0
// means the response was received but the body check failed, which is
// a very different problem than a 5xx.
function computeFailureReason(state: MonitorState): string | null {
  if (state.status !== "down") return null;

  if (state.tlsError) return "TLS error";
  if (state.dnsOk === false) return "DNS failure";

  if (state.httpStatusCode != null && state.httpStatusCode > 0) {
    const code = Math.round(state.httpStatusCode);
    if (code >= 200 && code < 400) {
      return `HTTP ${code} (body mismatch)`;
    }
    return `HTTP ${code}`;
  }

  return "timeout / unreachable";
}

function formatLastChecked(unixSec: number | null): string | null {
  if (unixSec == null) return null;
  const ageSec = Date.now() / 1000 - unixSec;
  if (ageSec < 0) return null;
  if (ageSec < 5) return "just now";
  if (ageSec < 60) return `${Math.floor(ageSec)}s ago`;
  if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
  if (ageSec < 86400) return `${Math.floor(ageSec / 3600)}h ago`;
  return `${Math.floor(ageSec / 86400)}d ago`;
}
