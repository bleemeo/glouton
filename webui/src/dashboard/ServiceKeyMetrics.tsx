import { Box, SimpleGrid, Text, VStack } from "@chakra-ui/react";
import { useMemo } from "react";

import { usePromQLRange } from "../api/hooks";
import { formatBytes, formatNumber, formatPercent } from "./format";
import { lastValue, samplesOf } from "./promql";
import { keyMetricsFor, type ServiceKeyMetric } from "./keyMetricsCatalog";
import { Sparkline } from "./Sparkline";

// Range used for the sparkline + current value (last sample within the
// range). 15 min @ 30 s step gives 30 points, enough to see a trend
// without overloading the drawer.
const RANGE_SECONDS = 15 * 60;
const STEP_SECONDS = 30;

function formatValue(metric: ServiceKeyMetric, v: number | null): string {
  if (v == null) return "—";

  switch (metric.format) {
    case "percent":
      return formatPercent(v);
    case "bytes":
      return formatBytes(v);
    case "count":
      return formatNumber(v, 0);
    case "rate":
      return `${formatNumber(v, 2)}/s`;
  }
}

type Props = {
  serviceName: string;
};

export function ServiceKeyMetrics({ serviceName }: Props) {
  const metrics = keyMetricsFor(serviceName);

  if (metrics.length === 0) {
    // The service type doesn't have a curated key-metric set; nothing
    // sensible to show.
    return null;
  }

  return (
    <VStack align="stretch" gap="2">
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        Key metrics
      </Text>
      <SimpleGrid columns={{ base: 1, sm: metrics.length > 2 ? 3 : 2 }} gap="2">
        {metrics.map((m) => (
          <MetricCard key={m.metric} metric={m} />
        ))}
      </SimpleGrid>
    </VStack>
  );
}

function MetricCard({ metric }: { metric: ServiceKeyMetric }) {
  // Glouton's per-service metrics are already scoped to a single
  // service (one input per service instance), so a bare metric name
  // is all that's needed — no {service="..."} selector required.
  const baseQuery = metric.metric;
  // For counters we want a per-second rate. The [1m] window matches
  // glouton's typical scrape interval and stays cheap.
  const query = metric.format === "rate" ? `rate(${baseQuery}[1m])` : baseQuery;

  const res = usePromQLRange(query, RANGE_SECONDS, STEP_SECONDS);
  const series = res.data?.data?.result?.[0];
  const samples = useMemo(() => samplesOf(series), [series]);
  const current = lastValue(res.data);

  // Hide cards where the metric simply doesn't exist (collector
  // disabled, service unreachable, etc.). This keeps the drawer
  // tight when only some of the curated set is actually emitted.
  const hasData = samples.length > 0 || current != null;

  if (res.loading && samples.length === 0) {
    return (
      <Box
        bg="surface.subtle"
        borderRadius="md"
        borderWidth="1px"
        borderColor="border.subtle"
        p="3"
        h="92px"
      >
        <Text
          fontSize="xs"
          color="fg.muted"
          textTransform="uppercase"
          letterSpacing="0.04em"
        >
          {metric.label}
        </Text>
        <Text fontSize="sm" color="fg.subtle" mt="2">
          Loading…
        </Text>
      </Box>
    );
  }

  if (!hasData) {
    return null;
  }

  return (
    <VStack
      align="start"
      gap="1"
      bg="surface.subtle"
      borderRadius="md"
      borderWidth="1px"
      borderColor="border.subtle"
      p="3"
    >
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.04em"
      >
        {metric.label}
      </Text>
      <Text
        fontSize="md"
        fontFamily="mono"
        fontVariantNumeric="tabular-nums"
        fontWeight="semibold"
      >
        {formatValue(metric, current)}
      </Text>
      <Box w="full" h="36px">
        {samples.length > 0 ? (
          <Sparkline data={samples} color={metric.color} yMax={metric.yMax} height={36} />
        ) : null}
      </Box>
    </VStack>
  );
}
