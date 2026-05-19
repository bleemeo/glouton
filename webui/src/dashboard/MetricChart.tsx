import { Box, chakra, HStack, Text, VStack } from "@chakra-ui/react";
import { useTheme } from "next-themes";
import { useEffect, useState, type ReactNode } from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { formatTickTime } from "./format";

type ZoomState = { left: number; right: number };

// Minimum span (as a fraction of the requested range) that a drag must
// cover to actually zoom — protects against accidental clicks.
const MIN_ZOOM_FRACTION = 0.01;

// Explicit hex colors per mode — Recharts passes the tick prop through
// SVG attributes where CSS custom properties resolve unreliably across
// browsers, so we feed it real values resolved at render time.
const TICK = { light: "#6B7280", dark: "#9CA3AF" } as const;
const AXIS = { light: "#D1D5DB", dark: "#2A3243" } as const;
const GRID = { light: "#E5E8EE", dark: "#1F2937" } as const;

function useChartPalette() {
  const { resolvedTheme } = useTheme();
  // resolvedTheme is undefined before hydration; fall back to dark to
  // avoid a light-themed flash on first paint of a system-dark user.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);

  const mode = mounted && resolvedTheme === "light" ? "light" : "dark";

  return { tick: TICK[mode], axis: AXIS[mode], grid: GRID[mode] };
}

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
  controls?: ReactNode;
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
  controls,
}: Props) {
  const palette = useChartPalette();

  // Drag-to-zoom: refLeft/refRight track the in-progress selection,
  // zoom holds the committed [left, right] window. Both are local to
  // this chart and reset on range change.
  const [refLeft, setRefLeft] = useState<number | null>(null);
  const [refRight, setRefRight] = useState<number | null>(null);
  const [zoom, setZoom] = useState<ZoomState | null>(null);

  useEffect(() => {
    // New range → throw away any zoom from the previous range; the
    // numeric bounds wouldn't make sense against the new dataset.
    setZoom(null);
    setRefLeft(null);
    setRefRight(null);
  }, [rangeSeconds]);

  const handleMouseDown = (state: { activeLabel?: number | string }) => {
    const label = numberOf(state?.activeLabel);
    if (label == null) return;
    setRefLeft(label);
    setRefRight(null);
  };

  const handleMouseMove = (state: { activeLabel?: number | string }) => {
    if (refLeft == null) return;
    const label = numberOf(state?.activeLabel);
    if (label == null) return;
    setRefRight(label);
  };

  const commitZoom = () => {
    if (refLeft != null && refRight != null && refLeft !== refRight) {
      const [a, b] = refLeft < refRight ? [refLeft, refRight] : [refRight, refLeft];
      if (b - a >= rangeSeconds * MIN_ZOOM_FRACTION) {
        setZoom({ left: a, right: b });
      }
    }
    setRefLeft(null);
    setRefRight(null);
  };

  const xDomain: [number, number] | ["dataMin", "dataMax"] = zoom
    ? [zoom.left, zoom.right]
    : ["dataMin", "dataMax"];

  // Click on a legend entry hides the matching series. Hidden state is
  // local to the chart and resets when the chart unmounts (e.g. range
  // change rebuilds the dataset).
  const [hidden, setHidden] = useState<Set<string>>(() => new Set());
  const toggleSeries = (key: string) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };
  // Recharts types dataKey as `string | number | (obj => any)`. We
  // only ever use string dataKeys so we narrow at the boundary.
  const handleLegendClick = (entry: unknown) => {
    const key = (entry as { dataKey?: unknown }).dataKey;
    if (typeof key === "string") toggleSeries(key);
  };
  const renderLegendLabel = (value: string, entry: unknown) => {
    const key = (entry as { dataKey?: unknown }).dataKey;
    const isHidden = typeof key === "string" && hidden.has(key);
    return (
      <span
        style={{
          color: isHidden ? palette.tick : "inherit",
          textDecoration: isHidden ? "line-through" : "none",
        }}
      >
        {value}
      </span>
    );
  };

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
      <HStack justify="space-between" mb="2" gap="3" align="start">
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
        <HStack gap="2">
          {zoom ? (
            <chakra.button
              type="button"
              onClick={() => setZoom(null)}
              fontSize="xs"
              fontFamily="mono"
              color="fg.muted"
              bg="surface.subtle"
              borderWidth="1px"
              borderColor="border.subtle"
              borderRadius="md"
              px="2"
              py="1"
              cursor="pointer"
              _hover={{ bg: "surface.canvas", color: "fg.default" }}
            >
              Reset zoom
            </chakra.button>
          ) : null}
          {controls}
        </HStack>
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
              <AreaChart
                data={data}
                margin={{ top: 8, right: 12, bottom: 4, left: 4 }}
                onMouseDown={handleMouseDown}
                onMouseMove={handleMouseMove}
                onMouseUp={commitZoom}
                onMouseLeave={commitZoom}
              >
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
                <CartesianGrid
                  strokeDasharray="2 4"
                  stroke={palette.grid}
                  vertical={false}
                />
                <XAxis
                  dataKey="t"
                  type="number"
                  domain={xDomain}
                  allowDataOverflow
                  tickFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
                  tick={{ fontSize: 10, fill: palette.tick, style: { fontSize: "10px" } }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  height={28}
                  minTickGap={48}
                />
                <YAxis
                  domain={yDomain ?? ["auto", "auto"]}
                  tick={{ fontSize: 10, fill: palette.tick, style: { fontSize: "10px" } }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  width={56}
                  tickFormatter={(v: number) =>
                    formatValue ? formatValue(v) : `${v}${unit ?? ""}`
                  }
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
                  formatter={tooltipFormatter(formatValue, unit)}
                />
                <Legend
                  wrapperStyle={{ ...legendStyle, cursor: "pointer" }}
                  iconType="circle"
                  iconSize={8}
                  onClick={handleLegendClick}
                  formatter={renderLegendLabel}
                />
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
                    hide={hidden.has(s.key)}
                  />
                ))}
                {refLeft != null && refRight != null ? (
                  <ReferenceArea
                    x1={refLeft}
                    x2={refRight}
                    strokeOpacity={0.3}
                    fill={palette.tick}
                    fillOpacity={0.15}
                  />
                ) : null}
              </AreaChart>
            ) : (
              <LineChart
                data={data}
                margin={{ top: 8, right: 12, bottom: 4, left: 4 }}
                onMouseDown={handleMouseDown}
                onMouseMove={handleMouseMove}
                onMouseUp={commitZoom}
                onMouseLeave={commitZoom}
              >
                <CartesianGrid
                  strokeDasharray="2 4"
                  stroke={palette.grid}
                  vertical={false}
                />
                <XAxis
                  dataKey="t"
                  type="number"
                  domain={xDomain}
                  allowDataOverflow
                  tickFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
                  tick={{ fontSize: 10, fill: palette.tick, style: { fontSize: "10px" } }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  height={28}
                  minTickGap={48}
                />
                <YAxis
                  domain={yDomain ?? ["auto", "auto"]}
                  tick={{ fontSize: 10, fill: palette.tick, style: { fontSize: "10px" } }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  width={56}
                  tickFormatter={(v: number) =>
                    formatValue ? formatValue(v) : `${v}${unit ?? ""}`
                  }
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(t: number) => formatTickTime(t, rangeSeconds)}
                  formatter={tooltipFormatter(formatValue, unit)}
                />
                <Legend
                  wrapperStyle={{ ...legendStyle, cursor: "pointer" }}
                  iconType="circle"
                  iconSize={8}
                  onClick={handleLegendClick}
                  formatter={renderLegendLabel}
                />
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
                    hide={hidden.has(s.key)}
                  />
                ))}
                {refLeft != null && refRight != null ? (
                  <ReferenceArea
                    x1={refLeft}
                    x2={refRight}
                    strokeOpacity={0.3}
                    fill={palette.tick}
                    fillOpacity={0.15}
                  />
                ) : null}
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


function numberOf(v: number | string | undefined): number | null {
  if (v == null) return null;
  const n = typeof v === "number" ? v : parseFloat(v);
  return isFinite(n) ? n : null;
}

function tooltipFormatter(formatValue?: (v: number) => string, unit?: string) {
  return (value: number, name: string) => {
    const formatted = formatValue
      ? formatValue(value)
      : `${value.toFixed(2)}${unit ?? ""}`;

    return [formatted, name];
  };
}
