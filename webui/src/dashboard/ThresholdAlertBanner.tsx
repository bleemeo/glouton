import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { Link as RouterLink } from "react-router-dom";

import {
  STATUS_COLORS,
  formatStatusTooltip,
  formatTimeSince,
  sinceForState,
  useThresholds,
} from "./thresholds";

/**
 * ThresholdAlertBanner shows a compact summary at the top of the
 * dashboard when at least one threshold is currently firing. The
 * banner lists the offending metrics by name (worst first) with a
 * "for Xm" duration so the reader can prioritise without scanning
 * the whole grid. Clicking the banner jumps to the Informations page
 * where the full thresholds table lives.
 *
 * Returns null (nothing rendered) when everything is ok — banners
 * that say "all good" become wallpaper.
 */
export function ThresholdAlertBanner() {
  const { states, summary, byMetric } = useThresholds();

  if (summary.warning === 0 && summary.critical === 0) return null;

  // Worst first; within a status, oldest "since" first (most chronic
  // problems get top billing). Use parsed epoch ms so a missing
  // timestamp (Infinity) sorts last instead of pretending to be the
  // earliest event ever.
  const sinceMs = (iso: string | null | undefined): number => {
    if (!iso) return Number.POSITIVE_INFINITY;
    const t = Date.parse(iso);
    return Number.isFinite(t) ? t : Number.POSITIVE_INFINITY;
  };

  const firing = states
    .filter((s) => s.status === "warning" || s.status === "critical")
    .sort((a, b) => {
      const rankA = a.status === "critical" ? 1 : 0;
      const rankB = b.status === "critical" ? 1 : 0;
      if (rankA !== rankB) return rankB - rankA;

      return sinceMs(sinceForState(a)) - sinceMs(sinceForState(b));
    });

  // Cap the visible list — past 5 items the banner gets crowded and
  // the reader should be opening the Informations page anyway.
  const TOP_N = 5;
  const visible = firing.slice(0, TOP_N);
  const extra = firing.length - visible.length;

  // Banner color follows the worst current state.
  const worst = summary.critical > 0 ? "crit" : "warn";
  const palette = {
    bg: STATUS_COLORS[worst].bgTint,
    border: STATUS_COLORS[worst].solid,
    fg: STATUS_COLORS[worst].solid,
  };

  const summaryText = (() => {
    const parts: string[] = [];
    if (summary.critical > 0) {
      parts.push(
        `${summary.critical} critical${summary.critical > 1 ? "s" : ""}`,
      );
    }
    if (summary.warning > 0) {
      parts.push(`${summary.warning} warning${summary.warning > 1 ? "s" : ""}`);
    }
    return parts.join(" · ");
  })();

  return (
    <Box
      bg={palette.bg}
      borderWidth="1px"
      borderColor={palette.border}
      borderRadius="lg"
      px="4"
      py="3"
    >
      <HStack align="start" gap="3" flexWrap="wrap">
        <VStack align="start" gap="0" minW="14ch">
          <Text
            fontSize="2xs"
            fontWeight="semibold"
            color={palette.fg}
            letterSpacing="0.08em"
            textTransform="uppercase"
          >
            Currently firing
          </Text>
          <Text fontSize="sm" fontFamily="mono" color="fg.default">
            {summaryText}
          </Text>
        </VStack>

        <HStack gap="2" wrap="wrap" flex="1">
          {visible.map((s) => {
            const color =
              s.status === "critical"
                ? STATUS_COLORS.crit.solid
                : STATUS_COLORS.warn.solid;
            const since = formatTimeSince(sinceForState(s) ?? undefined);
            // Prefer the rich "current X, threshold Y (+delta over warn)"
            // hint when a rule is in the lookup; degrade to the raw
            // labelsText (the previous behaviour) when no bounds match.
            const bounds = byMetric[s.metricName];
            const tooltip = bounds
              ? formatStatusTooltip(s, bounds, s.metricName)
              : null;

            return (
              <Box
                key={`${s.metricName}-${s.labelsText ?? ""}`}
                bg="surface.panel"
                borderWidth="1px"
                borderColor="border.subtle"
                borderRadius="md"
                px="2.5"
                py="1"
                display="inline-flex"
                alignItems="center"
                gap="2"
                title={tooltip ?? s.labelsText ?? s.metricName}
                cursor={tooltip ? "help" : undefined}
              >
                <Box w="1.5" h="1.5" borderRadius="full" bg={color} />
                <Text fontSize="xs" fontFamily="mono" color="fg.default">
                  {s.metricName}
                </Text>
                {s.item ? (
                  <Text
                    fontSize="2xs"
                    color="fg.muted"
                    fontFamily="mono"
                    maxW="16ch"
                    truncate
                  >
                    {s.item}
                  </Text>
                ) : null}
                {since ? (
                  <Text fontSize="2xs" color="fg.subtle" fontFamily="mono">
                    {since}
                  </Text>
                ) : null}
              </Box>
            );
          })}
          {extra > 0 ? (
            <Text fontSize="xs" color="fg.muted" alignSelf="center">
              + {extra} more
            </Text>
          ) : null}
        </HStack>

        <RouterLink
          to="/informations#active-thresholds"
          style={{ textDecoration: "none", marginLeft: "auto" }}
        >
          <Text
            fontSize="xs"
            color={palette.fg}
            fontWeight="medium"
            _hover={{ textDecoration: "underline" }}
          >
            View all →
          </Text>
        </RouterLink>
      </HStack>
    </Box>
  );
}
