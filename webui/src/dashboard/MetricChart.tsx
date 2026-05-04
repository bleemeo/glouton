import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import type { ReactNode } from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { formatTickTime } from "./format";

export type ChartSeries = {
  key: string;
  label: string;
  color: string;
  stack?: string;
};

type Props = {
  title: string;
  summary?: string;
  data: Array<Record<string, number>>;
  series: ChartSeries[];
  rangeSeconds: number;
  unit?: string;
  yDomain?: [number | "auto", number | "auto"];
  loading?: boolean;
  error?: Error | null;
  variant?: "area" | "line";
  formatValue?: (v: number) => string;
};

export function MetricChart({
  title,
  summary,
  data,
  series,
  rangeSeconds,
  unit,
  yDomain,
  loading,
  error,
  variant = "area",
  formatValue,
}: Props) {
  return (
    <Box
      bg="surface.panel"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="lg"
      p="4"
      h="320px"
      display="flex"
      flexDirection="column"
    >
      <HStack justify="space-between" mb="2">
        <VStack align="start" gap="0">
          <Text fontSize="sm" fontWeight="semibold">
            {title}
          </Text>
          {summary ? (
            <Text fontSize="xs" color="fg.muted" fontFamily="mono">
              {summary}
            </Text>
          ) : null}
        </VStack>
      </HStack>

      <Box flex="1" minH="0">
        {error ? (
          <CenteredMessage>
            <Text fontSize="sm" color="status.crit">
              Failed to load
            </Text>
            <Text fontSize="xs" color="fg.subtle" maxW="40ch" textAlign="center">
              {error.message}
            </Text>
          </CenteredMessage>
        ) : loading && data.length === 0 ? (
          <CenteredMessage>
            <Text fontSize="sm" color="fg.muted">
              Loading…
            </Text>
          </CenteredMessage>
        ) : data.length === 0 ? (
          <CenteredMessage>
            <Text fontSize="sm" color="fg.muted">
              No data
            </Text>
          </CenteredMessage>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            {variant === "area" ? (
              <AreaChart data={data} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
                <defs>
                  {series.map((s) => (
                    <linearGradient
                      key={`grad-${s.key}`}
                      id={`grad-${s.key}`}
                      x1="0"
                      y1="0"
                      x2="0"
                      y2="1"
                    >
                      <stop offset="0%" stopColor={s.color} stopOpacity={0.6} />
                      <stop offset="100%" stopColor={s.color} stopOpacity={0.05} />
                    </linearGradient>
                  ))}
                </defs>
                {axisAndGrid({ rangeSeconds, unit, yDomain, formatValue })}
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
                  formatter={tooltipFormatter(formatValue, unit)}
                />
                <Legend wrapperStyle={legendStyle} iconType="circle" iconSize={8} />
                {series.map((s) => (
                  <Area
                    key={s.key}
                    type="monotone"
                    dataKey={s.key}
                    name={s.label}
                    stroke={s.color}
                    fill={`url(#grad-${s.key})`}
                    strokeWidth={1.5}
                    dot={false}
                    isAnimationActive={false}
                    stackId={s.stack}
                  />
                ))}
              </AreaChart>
            ) : (
              <LineChart data={data} margin={{ top: 6, right: 8, bottom: 0, left: 0 }}>
                {axisAndGrid({ rangeSeconds, unit, yDomain, formatValue })}
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
                  formatter={tooltipFormatter(formatValue, unit)}
                />
                <Legend wrapperStyle={legendStyle} iconType="circle" iconSize={8} />
                {series.map((s) => (
                  <Line
                    key={s.key}
                    type="monotone"
                    dataKey={s.key}
                    name={s.label}
                    stroke={s.color}
                    strokeWidth={1.75}
                    dot={false}
                    isAnimationActive={false}
                  />
                ))}
              </LineChart>
            )}
          </ResponsiveContainer>
        )}
      </Box>
    </Box>
  );
}

function CenteredMessage({ children }: { children: ReactNode }) {
  return (
    <VStack h="full" justify="center" gap="1">
      {children}
    </VStack>
  );
}

const tooltipStyle = {
  background: "var(--chakra-colors-surface-panel)",
  border: "1px solid var(--chakra-colors-border-default)",
  borderRadius: "8px",
  fontSize: "12px",
};

const legendStyle = {
  fontSize: "11px",
  paddingTop: "4px",
};

const axisStyle = {
  fontSize: 11,
  fill: "var(--chakra-colors-fg-subtle)",
};

function axisAndGrid({
  rangeSeconds,
  unit,
  yDomain,
  formatValue,
}: {
  rangeSeconds: number;
  unit?: string;
  yDomain?: [number | "auto", number | "auto"];
  formatValue?: (v: number) => string;
}) {
  return (
    <>
      <CartesianGrid
        strokeDasharray="2 4"
        stroke="var(--chakra-colors-border-subtle)"
        vertical={false}
      />
      <XAxis
        dataKey="t"
        type="number"
        domain={["dataMin", "dataMax"]}
        scale="time"
        tickFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
        tick={axisStyle}
        stroke="var(--chakra-colors-border-default)"
        minTickGap={48}
      />
      <YAxis
        domain={yDomain ?? ["auto", "auto"]}
        tick={axisStyle}
        stroke="var(--chakra-colors-border-default)"
        width={48}
        tickFormatter={(v: number) => (formatValue ? formatValue(v) : `${v}${unit ?? ""}`)}
      />
    </>
  );
}

function tooltipFormatter(formatValue?: (v: number) => string, unit?: string) {
  return (value: number, name: string) => {
    const formatted = formatValue
      ? formatValue(value)
      : `${value.toFixed(2)}${unit ?? ""}`;

    return [formatted, name];
  };
}
