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
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { formatTickTime } from "./format";
import { STATUS_COLORS, type ThresholdBounds } from "./thresholds";

// Solid colors used by the warning / critical reference lines.
const WARN_COLOR = STATUS_COLORS.warn.solid;
const CRIT_COLOR = STATUS_COLORS.crit.solid;

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
  // thresholds, when set, are drawn as faint horizontal warn/crit
  // reference zones. Useful on KPI-style charts (CPU%, RAM%) where
  // the bounds tell the reader at a glance how much headroom remains.
  thresholds?: ThresholdBounds;
  // currentStatus, when "warn" or "crit", surfaces a small pill next
  // to the chart title. Lets the reader spot a firing chart in a
  // grid of cards without inspecting the data lines.
  currentStatus?: "ok" | "warn" | "crit" | "unknown";
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
  thresholds,
  currentStatus,
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
      const [a, b] =
        refLeft < refRight ? [refLeft, refRight] : [refRight, refLeft];
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
          <HStack gap="2" align="center">
            <Text fontSize="sm" fontWeight="semibold">
              {title}
            </Text>
            {currentStatus === "warn" || currentStatus === "crit" ? (
              <StatusPill status={currentStatus} />
            ) : null}
          </HStack>
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
            <Text
              fontSize="xs"
              color="fg.subtle"
              maxW="40ch"
              textAlign="center"
            >
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
                      <stop
                        offset="100%"
                        stopColor={s.color}
                        stopOpacity={0.05}
                      />
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
                  tick={{
                    fontSize: 10,
                    fill: palette.tick,
                    style: { fontSize: "10px" },
                  }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  height={28}
                  minTickGap={48}
                />
                <YAxis
                  domain={yDomain ?? ["auto", "auto"]}
                  tick={{
                    fontSize: 10,
                    fill: palette.tick,
                    style: { fontSize: "10px" },
                  }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  width={56}
                  tickFormatter={(v: number) =>
                    formatValue ? formatValue(v) : `${v}${unit ?? ""}`
                  }
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(t: number) =>
                    formatTickTime(t, rangeSeconds)
                  }
                  formatter={tooltipFormatter(formatValue, unit)}
                  content={
                    thresholds
                      ? (props) => (
                          <ThresholdAwareTooltip
                            {...props}
                            rangeSeconds={rangeSeconds}
                            formatValue={formatValue}
                            unit={unit}
                            thresholds={thresholds}
                          />
                        )
                      : undefined
                  }
                />
                <Legend
                  wrapperStyle={{ ...legendStyle, cursor: "pointer" }}
                  iconType="circle"
                  iconSize={8}
                  onClick={handleLegendClick}
                  formatter={renderLegendLabel}
                />
                {renderThresholdBands(thresholds, formatValue)}
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
                  tick={{
                    fontSize: 10,
                    fill: palette.tick,
                    style: { fontSize: "10px" },
                  }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  height={28}
                  minTickGap={48}
                />
                <YAxis
                  domain={yDomain ?? ["auto", "auto"]}
                  tick={{
                    fontSize: 10,
                    fill: palette.tick,
                    style: { fontSize: "10px" },
                  }}
                  stroke={palette.axis}
                  tickLine={{ stroke: palette.axis }}
                  width={56}
                  tickFormatter={(v: number) =>
                    formatValue ? formatValue(v) : `${v}${unit ?? ""}`
                  }
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={(t: number) =>
                    formatTickTime(t, rangeSeconds)
                  }
                  formatter={tooltipFormatter(formatValue, unit)}
                  content={
                    thresholds
                      ? (props) => (
                          <ThresholdAwareTooltip
                            {...props}
                            rangeSeconds={rangeSeconds}
                            formatValue={formatValue}
                            unit={unit}
                            thresholds={thresholds}
                          />
                        )
                      : undefined
                  }
                />
                <Legend
                  wrapperStyle={{ ...legendStyle, cursor: "pointer" }}
                  iconType="circle"
                  iconSize={8}
                  onClick={handleLegendClick}
                  formatter={renderLegendLabel}
                />
                {renderThresholdBands(thresholds, formatValue)}
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

// StatusPill is the compact "WARN" / "CRIT" tag rendered next to a
// chart title when the metric the chart represents is currently
// firing. Mirrors the visual language of the dashboard banner so the
// two reinforce each other.
function StatusPill({ status }: { status: "warn" | "crit" }) {
  const color = status === "crit" ? CRIT_COLOR : WARN_COLOR;
  const label = status === "crit" ? "CRIT" : "WARN";

  return (
    <Box
      as="span"
      display="inline-flex"
      alignItems="center"
      gap="1.5"
      px="1.5"
      py="0.5"
      borderRadius="sm"
      bg={`${color}1F`}
      title={
        status === "crit"
          ? "Metric in critical state"
          : "Metric in warning state"
      }
    >
      <Box w="1.5" h="1.5" borderRadius="full" bg={color} />
      <Text
        as="span"
        fontSize="2xs"
        fontWeight="semibold"
        color={color}
        letterSpacing="0.04em"
      >
        {label}
      </Text>
    </Box>
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

// thresholdStatusFor folds the hovered data point against the chart's
// bounds. For stacked charts (where the displayed Y is a sum) we sum
// all series at the hovered timestamp; for line charts we use the
// single series value. Returns null when no bound is crossed.
function thresholdStatusFor(
  total: number,
  bounds: ThresholdBounds,
): { level: "warn" | "crit"; label: string } | null {
  if (bounds.highCritical != null && total >= bounds.highCritical) {
    return { level: "crit", label: "above critical" };
  }
  if (bounds.lowCritical != null && total <= bounds.lowCritical) {
    return { level: "crit", label: "below critical" };
  }
  if (bounds.highWarning != null && total >= bounds.highWarning) {
    return { level: "warn", label: "above warning" };
  }
  if (bounds.lowWarning != null && total <= bounds.lowWarning) {
    return { level: "warn", label: "below warning" };
  }
  return null;
}

// ThresholdAwareTooltip is a custom recharts Tooltip content that
// mimics the default look-and-feel and appends a status line when
// the cursor is hovering over a data point that crosses one of the
// configured thresholds. We render the existing series rows ourselves
// so the threshold footer can sit at the bottom in a consistent style.
// Recharts' TooltipPayloadEntry has a wide `value` type
// (string | number | (string | number)[]) and uses ValueType
// internally. We accept `readonly unknown[]` and narrow each entry
// inside rather than importing recharts internals.
function ThresholdAwareTooltip(props: {
  active?: boolean;
  payload?: readonly unknown[];
  label?: number | string;
  rangeSeconds: number;
  formatValue?: (v: number) => string;
  unit?: string;
  thresholds: ThresholdBounds;
}) {
  const {
    active,
    payload,
    label,
    rangeSeconds,
    formatValue,
    unit,
    thresholds,
  } = props;

  if (!active || !payload || payload.length === 0) return null;

  // Narrow each entry at the boundary. Anything that isn't a numeric
  // value contributes 0 to the total so stacked rendering doesn't
  // break when recharts hands us a placeholder series.
  const entries = payload.map((raw) => {
    const p = raw as {
      color?: string;
      name?: string | number;
      value?: number | string | unknown;
      dataKey?: string | number;
    };
    const numeric = typeof p.value === "number" ? p.value : null;
    return {
      color: p.color,
      name: p.name,
      value: numeric,
      dataKey: p.dataKey,
    };
  });

  const total = entries.reduce((acc, p) => acc + (p.value ?? 0), 0);
  const status = thresholdStatusFor(total, thresholds);

  const labelText =
    typeof label === "number" ? formatTickTime(label, rangeSeconds) : label;

  return (
    <div
      style={{
        background: "var(--chakra-colors-surface-panel)",
        border: "1px solid var(--chakra-colors-border-default)",
        borderRadius: "8px",
        fontSize: "12px",
        padding: "8px 10px",
        minWidth: "120px",
      }}
    >
      <div
        style={{
          fontSize: "11px",
          color: "var(--chakra-colors-fg-muted)",
          marginBottom: "4px",
        }}
      >
        {labelText}
      </div>
      {entries.map((p, i) => {
        const value =
          p.value == null
            ? "—"
            : formatValue
              ? formatValue(p.value)
              : `${p.value.toFixed(2)}${unit ?? ""}`;
        return (
          <div
            key={`${p.dataKey ?? "k"}-${i}`}
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              padding: "1px 0",
            }}
          >
            <span
              style={{
                display: "inline-block",
                width: 8,
                height: 8,
                borderRadius: "50%",
                background: p.color,
              }}
            />
            <span style={{ color: "var(--chakra-colors-fg-default)" }}>
              {p.name}
            </span>
            <span
              style={{
                marginLeft: "auto",
                fontFamily:
                  "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
                color: "var(--chakra-colors-fg-default)",
              }}
            >
              {value}
            </span>
          </div>
        );
      })}
      {status ? (
        <div
          style={{
            marginTop: "6px",
            paddingTop: "6px",
            borderTop: "1px solid var(--chakra-colors-border-subtle)",
            fontSize: "11px",
            fontWeight: 600,
            color: status.level === "crit" ? CRIT_COLOR : WARN_COLOR,
            textTransform: "uppercase",
            letterSpacing: "0.04em",
          }}
        >
          {status.label}
        </div>
      ) : null}
    </div>
  );
}

// renderThresholdBands draws dashed reference lines at each
// configured warn/crit boundary, with a discrete value-only label on
// the right edge of the chart. Earlier iterations had:
//   - translucent fill bands → blended into data, hard to read.
//   - "warn 75%" / "crit 90%" inline labels → too large, redundant
//     with the colour, and the Y axis already shows where the line
//     sits.
// The current style keeps the line minimal and lets the dashed colour
// carry the semantics (orange = warn, red = crit); the small label
// repeats only the *value* so the reader doesn't have to interpolate
// it from the Y axis when the threshold falls between two ticks.
function renderThresholdBands(
  bounds: ThresholdBounds | undefined,
  formatValue?: (v: number) => string,
) {
  if (!bounds) return null;

  const lines: ReactNode[] = [];

  const addLine = (key: string, value: number, color: string) => {
    const text = formatValue ? formatValue(value) : String(value);
    lines.push(
      <ReferenceLine
        key={key}
        y={value}
        stroke={color}
        strokeDasharray="3 3"
        strokeWidth={1}
        strokeOpacity={0.7}
        ifOverflow="extendDomain"
        label={renderThresholdLabel(text, color)}
      />,
    );
  };

  if (bounds.highCritical != null) {
    addLine("high-crit", bounds.highCritical, CRIT_COLOR);
  }

  if (bounds.highWarning != null) {
    addLine("high-warn", bounds.highWarning, WARN_COLOR);
  }

  if (bounds.lowWarning != null) {
    addLine("low-warn", bounds.lowWarning, WARN_COLOR);
  }

  if (bounds.lowCritical != null) {
    addLine("low-crit", bounds.lowCritical, CRIT_COLOR);
  }

  return lines;
}

// renderThresholdLabel renders a minimal SVG label aligned to the
// right of the chart, just above the reference line. We deliberately
// keep it text-only (no pill, no background) so the threshold reads
// like an axis annotation rather than a competing UI chip. Font
// styling goes through the `style` prop because Recharts injects CSS
// that overrides bare SVG `font-size` / `font-family` attributes.
function renderThresholdLabel(text: string, color: string) {
  const ThresholdLabel = (props: {
    viewBox?: { x?: number; width?: number; y?: number };
  }) => {
    const vb = props.viewBox ?? {};
    const x = (vb.x ?? 0) + (vb.width ?? 0) - 4;
    const y = (vb.y ?? 0) - 3;

    return (
      <text
        x={x}
        y={y}
        textAnchor="end"
        style={{
          fontSize: "9px",
          fontFamily:
            "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
          fontWeight: 600,
          fill: color,
          letterSpacing: "0.02em",
        }}
      >
        {text}
      </text>
    );
  };
  ThresholdLabel.displayName = "ThresholdLabel";

  return ThresholdLabel;
}
