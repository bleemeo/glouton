import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import type { ReactNode } from "react";

import type { Status } from "../app/StatusBadge";

type Props = {
  label: string;
  value: string;
  hint?: string;
  // statusHint is a short "WARNING for 12m" style line shown above the
  // regular hint when the status is warn or crit. Rendered in the
  // status color to draw the eye to actionable cards first.
  statusHint?: string;
  // tooltip is shown as a native title on hover — typically the
  // current-value + threshold breakdown produced by formatStatusTooltip.
  tooltip?: string;
  fillRatio?: number; // 0..1 for the progress fill, undefined = no fill
  status: Status;
  trailing?: ReactNode;
};

const statusToTokens: Record<Status, { bg: string; ring: string; fg: string }> =
  {
    ok: { bg: "kpi.ok.bg", ring: "kpi.ok.ring", fg: "kpi.ok.fg" },
    warn: { bg: "kpi.warn.bg", ring: "kpi.warn.ring", fg: "kpi.warn.fg" },
    crit: { bg: "kpi.crit.bg", ring: "kpi.crit.ring", fg: "kpi.crit.fg" },
    unknown: { bg: "kpi.neutral.bg", ring: "kpi.neutral.ring", fg: "fg.muted" },
  };

export function KPICard({
  label,
  value,
  hint,
  statusHint,
  tooltip,
  fillRatio,
  status,
  trailing,
}: Props) {
  const tokens = statusToTokens[status];
  const ratio = fillRatio == null ? null : Math.max(0, Math.min(1, fillRatio));

  return (
    <Box
      bg="surface.panel"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="lg"
      px="4"
      py="3"
      position="relative"
      overflow="hidden"
      _hover={{ borderColor: "border.default" }}
      transition="border-color 120ms ease"
      title={tooltip}
      cursor={tooltip ? "help" : undefined}
    >
      {ratio !== null ? (
        <Box
          position="absolute"
          top={0}
          left={0}
          h="3px"
          w={`${ratio * 100}%`}
          bg={tokens.ring}
          opacity={status === "unknown" ? 0.3 : 0.9}
          transition="width 240ms ease"
        />
      ) : null}

      <VStack align="start" gap="1.5">
        <HStack justify="space-between" w="full">
          <Text
            fontSize="xs"
            fontWeight="semibold"
            color="fg.muted"
            letterSpacing="0.08em"
            textTransform="uppercase"
          >
            {label}
          </Text>
          {trailing}
        </HStack>

        <Text
          fontSize="2xl"
          fontWeight="bold"
          fontFamily="mono"
          fontVariantNumeric="tabular-nums"
          color={status === "unknown" ? "fg.default" : tokens.fg}
          lineHeight="1.1"
        >
          {value}
        </Text>

        {statusHint && (status === "warn" || status === "crit") ? (
          <Text fontSize="xs" color={tokens.fg} fontFamily="mono">
            {statusHint}
          </Text>
        ) : null}

        {hint ? (
          <Text fontSize="xs" color="fg.subtle">
            {hint}
          </Text>
        ) : null}
      </VStack>
    </Box>
  );
}
