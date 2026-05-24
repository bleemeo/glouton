import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useMemo } from "react";
import { LuShieldAlert } from "react-icons/lu";

import type { Monitor } from "../api/types";
import type { MonitorState } from "./useMonitorStates";

// Threshold below which a cert is surfaced in the banner. Within this
// window we treat <1d as critical and <14d as warning.
const WARN_DAYS = 14;
const CRIT_DAYS = 1;

type ExpiringCert = {
  monitor: Monitor;
  remainingDays: number;
  level: "warn" | "crit";
};

type Props = {
  states: MonitorState[];
  onMonitorClick?: (monitor: Monitor) => void;
};

export function CertExpiryBanner({ states, onMonitorClick }: Props) {
  const expiring: ExpiringCert[] = useMemo(() => {
    const nowSec = Date.now() / 1000;
    const out: ExpiringCert[] = [];

    for (const s of states) {
      if (s.monitor.scheme !== "https") continue;

      // probe_ssl_last_chain_expiry_timestamp_seconds is 0 (zero
      // time.Time) when the SSL handshake failed entirely — skip
      // those, the down-state is already shown on the card.
      if (s.certExpiryUnix == null || s.certExpiryUnix <= 0) continue;

      const remainingDays = (s.certExpiryUnix - nowSec) / 86_400;
      if (remainingDays >= WARN_DAYS) continue;

      out.push({
        monitor: s.monitor,
        remainingDays,
        level: remainingDays < CRIT_DAYS ? "crit" : "warn",
      });
    }

    return out.sort((a, b) => a.remainingDays - b.remainingDays);
  }, [states]);

  if (expiring.length === 0) return null;

  const worst = expiring[0].level;
  const palette =
    worst === "crit"
      ? { bg: "rgba(239, 68, 68, 0.08)", border: "#EF4444", fg: "#EF4444" }
      : { bg: "rgba(245, 158, 11, 0.08)", border: "#F59E0B", fg: "#F59E0B" };

  return (
    <Box
      bg={palette.bg}
      borderWidth="1px"
      borderColor={palette.border}
      borderRadius="lg"
      p="3"
    >
      <HStack align="start" gap="3">
        <Box color={palette.fg} pt="0.5" fontSize="lg">
          <LuShieldAlert />
        </Box>
        <VStack align="stretch" gap="2" flex="1" minW="0">
          <Text fontSize="sm" fontWeight="semibold">
            {expiring.length === 1
              ? "1 certificate expires soon"
              : `${expiring.length} certificates expire soon`}
          </Text>
          <VStack align="stretch" gap="1">
            {expiring.map(({ monitor, remainingDays, level }) => (
              <CertRow
                key={monitor.url}
                monitor={monitor}
                remainingDays={remainingDays}
                level={level}
                onClick={
                  onMonitorClick ? () => onMonitorClick(monitor) : undefined
                }
              />
            ))}
          </VStack>
        </VStack>
      </HStack>
    </Box>
  );
}

function CertRow({
  monitor,
  remainingDays,
  level,
  onClick,
}: {
  monitor: Monitor;
  remainingDays: number;
  level: "warn" | "crit";
  onClick?: () => void;
}) {
  const color = level === "crit" ? "#EF4444" : "#F59E0B";
  const remainingLabel =
    remainingDays < 0
      ? "expired"
      : remainingDays < 1
        ? `${Math.max(0, Math.round(remainingDays * 24))}h`
        : `${Math.round(remainingDays)}d`;

  return (
    <HStack
      gap="3"
      fontSize="xs"
      fontFamily="mono"
      fontVariantNumeric="tabular-nums"
      cursor={onClick ? "pointer" : "default"}
      onClick={onClick}
      _hover={onClick ? { color: "fg.default" } : undefined}
      transition="color 100ms ease"
    >
      <Text color={color} fontWeight="semibold" minW="5ch" textAlign="end">
        {remainingLabel}
      </Text>
      <Text color="fg.default" truncate flex="1">
        {monitor.name}
      </Text>
      <Text color="fg.subtle" truncate flex="2" textAlign="end">
        {monitor.url}
      </Text>
    </HStack>
  );
}
