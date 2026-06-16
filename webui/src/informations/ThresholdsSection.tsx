import {
  Box,
  chakra,
  Heading,
  HStack,
  Spinner,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react";
import { useEffect, useMemo, useState, type ReactNode } from "react";

import type { ThresholdRule, ThresholdState } from "../api/types";
import {
  SOURCE_COLORS,
  STATUS_COLORS,
  formatBoundValue,
  formatStatusTooltip,
  formatTimeSince,
  sinceForState,
  statusRank,
  useThresholds,
} from "../dashboard/thresholds";

type ThresholdRow = {
  rule: ThresholdRule;
  // The state corresponding to this row, if any. For per-item
  // expansion (config rules tracking multiple series), each row pins
  // a specific state and the row's `item` reflects that state's item
  // rather than the rule's (which is empty).
  state: ThresholdState | undefined;
  // Item shown on the row. May come from either the rule (Bleemeo
  // per-instance) or from the state (config rule × actual series).
  item: string;
  // React key — composed of metric, item and rule-source to be unique
  // even when the same (metric, item) pair has multiple rules.
  key: string;
};

type SortKey = "status" | "metric" | "source";
type SortDir = "asc" | "desc";

export function ThresholdsSection() {
  const { rules, states, loading, error } = useThresholds();

  const [sortKey, setSortKey] = useState<SortKey>("status");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      // Default direction per column: status worst-first, name/source
      // alphabetical ascending.
      setSortDir(key === "status" ? "desc" : "asc");
    }
  };

  // Build the row list: one row per (rule × matching state). Config
  // rules with N firing/tracked items expand into N rows; Bleemeo
  // rules that already carry a labelsText stay as a single row.
  const rows = useMemo<ThresholdRow[]>(() => {
    // Group all states by metric name so we can find every series
    // a config rule applies to.
    const statesByMetric: Record<string, ThresholdState[]> = {};
    for (const s of states) {
      (statesByMetric[s.metricName] ??= []).push(s);
    }

    const out: ThresholdRow[] = [];

    for (const r of rules) {
      if (r.labelsText) {
        // Per-instance rule (Bleemeo) — find the matching state by
        // exact labels, expose the rule's own item.
        const state = states.find(
          (s) => s.metricName === r.metricName && s.labelsText === r.labelsText,
        );
        out.push({
          rule: r,
          state,
          item: r.item ?? "",
          key: `${r.metricName}|${r.labelsText}`,
        });
        continue;
      }

      // Config rule (no labels) — fan out to every state for this
      // metric name. If no state tracks the metric yet (typical when
      // glouton just started and hasn't applied the threshold once),
      // we still show one row with no state.
      const matched = statesByMetric[r.metricName] ?? [];
      if (matched.length === 0) {
        out.push({
          rule: r,
          state: undefined,
          item: "",
          key: r.metricName,
        });
        continue;
      }

      for (const s of matched) {
        out.push({
          rule: r,
          state: s,
          item: s.item ?? "",
          key: `${r.metricName}|${s.labelsText ?? ""}`,
        });
      }
    }

    const dir = sortDir === "asc" ? 1 : -1;
    return out.sort((a, b) => {
      let cmp = 0;
      if (sortKey === "status") {
        cmp = statusRank(a.state?.status) - statusRank(b.state?.status);
      } else if (sortKey === "metric") {
        cmp = a.rule.metricName.localeCompare(b.rule.metricName);
      } else if (sortKey === "source") {
        cmp = a.rule.source.localeCompare(b.rule.source);
      }
      if (cmp === 0) {
        // Stable secondary sort by metric+item so per-item rows
        // remain grouped under their metric.
        cmp = a.rule.metricName.localeCompare(b.rule.metricName);
        if (cmp === 0) cmp = a.item.localeCompare(b.item);
        return cmp;
      }
      return cmp * dir;
    });
  }, [rules, states, sortKey, sortDir]);

  // By default the section only shows what's currently firing so the
  // user doesn't scroll past 50 ok-rows to find the warnings. Click on
  // "View all" expands to the full list. Cap firing at FIRING_CAP so a
  // pathological state (everything red) doesn't blow up the page.
  const FIRING_CAP = 25;
  const firingRows = rows.filter(
    (r) => r.state?.status === "warning" || r.state?.status === "critical",
  );

  const [expanded, setExpanded] = useState(false);
  const [filter, setFilter] = useState("");

  // When the dashboard "Currently firing" banner deep-links here,
  // auto-expand the full table and scroll the section into view so
  // the user lands on the rows they came to see.
  useEffect(() => {
    if (window.location.hash !== "#active-thresholds") return;
    setExpanded(true);
    document
      .getElementById("active-thresholds")
      ?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, []);

  const matchesFilter = (row: ThresholdRow): boolean => {
    const q = filter.trim().toLowerCase();
    if (!q) return true;
    return (
      row.rule.metricName.toLowerCase().includes(q) ||
      row.item.toLowerCase().includes(q)
    );
  };

  const baseRows = expanded ? rows : firingRows.slice(0, FIRING_CAP);
  const displayed = filter ? baseRows.filter(matchesFilter) : baseRows;
  const firingTruncated = !expanded && firingRows.length > FIRING_CAP;

  return (
    <VStack align="stretch" gap="2" id="active-thresholds" scrollMarginTop="4">
      <Heading
        size="sm"
        color="fg.muted"
        letterSpacing="0.06em"
        textTransform="uppercase"
      >
        Active thresholds
      </Heading>
      <Box
        bg="surface.panel"
        borderWidth="1px"
        borderColor="border.subtle"
        borderRadius="lg"
        overflow="hidden"
      >
        {error ? (
          <Box p="6" color="status.crit">
            Failed to load thresholds: {error.message}
          </Box>
        ) : loading && rules.length === 0 ? (
          <HStack p="6" gap="3">
            <Spinner size="sm" />
            <Text color="fg.muted">Loading…</Text>
          </HStack>
        ) : rules.length === 0 ? (
          <Box p="6" color="fg.muted" fontSize="sm">
            No threshold is currently active. Configure them under{" "}
            <Text as="code" fontFamily="mono">
              thresholds
            </Text>{" "}
            in your agent config, or register with Bleemeo Cloud to receive them
            from the platform.
          </Box>
        ) : (
          <>
            {expanded ? (
              <Box
                px="4"
                py="2.5"
                borderBottomWidth="1px"
                borderColor="border.subtle"
              >
                <chakra.input
                  type="text"
                  placeholder="Filter by metric name or item…"
                  value={filter}
                  onChange={(e) => setFilter(e.currentTarget.value)}
                  w="100%"
                  maxW="320px"
                  px="2.5"
                  py="1"
                  fontSize="sm"
                  fontFamily="mono"
                  bg="surface.subtle"
                  borderWidth="1px"
                  borderColor="border.subtle"
                  borderRadius="md"
                  _focus={{
                    outline: "none",
                    borderColor: "border.default",
                    bg: "surface.panel",
                  }}
                />
              </Box>
            ) : null}
            {!expanded && firingRows.length === 0 ? (
              <Box
                px="4"
                py="3"
                borderBottomWidth="1px"
                borderColor="border.subtle"
                fontSize="sm"
                color="fg.muted"
              >
                <HStack justify="space-between" gap="3" wrap="wrap">
                  <Text>All {rows.length} thresholds are currently OK.</Text>
                  <chakra.button
                    type="button"
                    onClick={() => setExpanded(true)}
                    fontSize="xs"
                    fontWeight="medium"
                    color="fg.default"
                    bg="surface.subtle"
                    borderWidth="1px"
                    borderColor="border.default"
                    borderRadius="md"
                    px="3"
                    py="1"
                    cursor="pointer"
                    _hover={{ bg: "surface.canvas" }}
                  >
                    View all →
                  </chakra.button>
                </HStack>
              </Box>
            ) : null}
            {displayed.length > 0 ? (
              <Table.Root size="sm" variant="line">
                <Table.Header bg="surface.subtle">
                  <Table.Row>
                    <SortableHeader
                      label="Status"
                      column="status"
                      activeKey={sortKey}
                      dir={sortDir}
                      onClick={toggleSort}
                    />
                    <SortableHeader
                      label="Metric"
                      column="metric"
                      activeKey={sortKey}
                      dir={sortDir}
                      onClick={toggleSort}
                    />
                    <SortableHeader
                      label="Source"
                      column="source"
                      activeKey={sortKey}
                      dir={sortDir}
                      onClick={toggleSort}
                    />
                    <Table.ColumnHeader>Low crit / warn</Table.ColumnHeader>
                    <Table.ColumnHeader>High warn / crit</Table.ColumnHeader>
                    <Table.ColumnHeader>Soft delay</Table.ColumnHeader>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {displayed.map((row) => {
                    const { rule: r, state, item } = row;
                    return (
                      <Table.Row
                        key={row.key}
                        bg={
                          state?.status === "critical"
                            ? STATUS_COLORS.crit.bgTint
                            : state?.status === "warning"
                              ? STATUS_COLORS.warn.bgTint
                              : undefined
                        }
                        transition="background-color 100ms ease"
                        _hover={{
                          bg:
                            state?.status === "critical"
                              ? STATUS_COLORS.crit.bgHover
                              : state?.status === "warning"
                                ? STATUS_COLORS.warn.bgHover
                                : "surface.subtle",
                        }}
                      >
                        <Table.Cell>
                          <StatusCell state={state} rule={r} />
                        </Table.Cell>
                        <Table.Cell
                          fontWeight="medium"
                          fontFamily="mono"
                          fontSize="sm"
                        >
                          <HStack gap="1.5" align="baseline">
                            <Text as="span">{r.metricName}</Text>
                            {item ? <ItemChip item={item} /> : null}
                          </HStack>
                        </Table.Cell>
                        <Table.Cell>
                          <SourceBadge source={r.source} />
                        </Table.Cell>
                        <Table.Cell
                          fontFamily="mono"
                          fontSize="sm"
                          color="fg.muted"
                        >
                          {formatBound(
                            r.lowCritical,
                            STATUS_COLORS.crit.solid,
                            r.metricName,
                          )}{" "}
                          /{" "}
                          {formatBound(
                            r.lowWarning,
                            STATUS_COLORS.warn.solid,
                            r.metricName,
                          )}
                        </Table.Cell>
                        <Table.Cell
                          fontFamily="mono"
                          fontSize="sm"
                          color="fg.muted"
                        >
                          {formatBound(
                            r.highWarning,
                            STATUS_COLORS.warn.solid,
                            r.metricName,
                          )}{" "}
                          /{" "}
                          {formatBound(
                            r.highCritical,
                            STATUS_COLORS.crit.solid,
                            r.metricName,
                          )}
                        </Table.Cell>
                        <Table.Cell
                          fontFamily="mono"
                          fontSize="xs"
                          color="fg.subtle"
                        >
                          {formatDelay(r.warningDelaySec, r.criticalDelaySec)}
                        </Table.Cell>
                      </Table.Row>
                    );
                  })}
                </Table.Body>
              </Table.Root>
            ) : expanded && filter ? (
              <Box px="4" py="6" fontSize="sm" color="fg.muted">
                No threshold matches{" "}
                <Text as="code" fontFamily="mono">
                  “{filter}”
                </Text>
                .
              </Box>
            ) : null}
            {firingRows.length > 0 || expanded ? (
              <Box
                px="4"
                py="2.5"
                borderTopWidth="1px"
                borderColor="border.subtle"
                bg="surface.subtle"
              >
                <HStack justify="space-between" gap="3" wrap="wrap">
                  <Text fontSize="xs" color="fg.muted">
                    {expanded
                      ? filter
                        ? `Matching ${displayed.length} of ${rows.length} threshold${rows.length === 1 ? "" : "s"}.`
                        : `Showing all ${rows.length} threshold${rows.length === 1 ? "" : "s"}.`
                      : firingTruncated
                        ? `Showing ${FIRING_CAP} of ${firingRows.length} firing thresholds.`
                        : `Showing ${displayed.length} firing threshold${displayed.length === 1 ? "" : "s"}; ${rows.length - displayed.length} more OK.`}
                  </Text>
                  <chakra.button
                    type="button"
                    onClick={() => setExpanded((v) => !v)}
                    fontSize="xs"
                    fontWeight="medium"
                    color="fg.default"
                    bg="surface.panel"
                    borderWidth="1px"
                    borderColor="border.default"
                    borderRadius="md"
                    px="3"
                    py="1"
                    cursor="pointer"
                    _hover={{ bg: "surface.canvas" }}
                  >
                    {expanded ? "Show firing only" : "View all"}
                  </chakra.button>
                </HStack>
              </Box>
            ) : null}
          </>
        )}
      </Box>
    </VStack>
  );
}

// ItemChip renders the per-item discriminator (mountpoint, interface,
// …) as a compact chip alongside the metric name. We render it as a
// "bracket"-style chip rather than yet-another-pill so it reads as a
// label refinement, not as a separate concept.
function ItemChip({ item }: { item: string }) {
  return (
    <Box
      as="span"
      display="inline-flex"
      alignItems="center"
      px="1.5"
      py="0.5"
      bg="surface.subtle"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="sm"
      fontSize="2xs"
      fontFamily="mono"
      color="fg.muted"
      maxW="20ch"
      title={item}
    >
      <Text as="span" truncate>
        {item}
      </Text>
    </Box>
  );
}

function StatusCell({
  state,
  rule,
}: {
  state: ThresholdState | undefined;
  rule: ThresholdRule;
}) {
  if (!state) {
    return (
      <Text as="span" fontSize="xs" color="fg.subtle">
        —
      </Text>
    );
  }

  const colors: Record<ThresholdState["status"], string> = {
    ok: STATUS_COLORS.ok.solid,
    warning: STATUS_COLORS.warn.solid,
    critical: STATUS_COLORS.crit.solid,
    unknown: STATUS_COLORS.unknown.solid,
  };
  const labels: Record<ThresholdState["status"], string> = {
    ok: "OK",
    warning: "WARN",
    critical: "CRIT",
    unknown: "?",
  };

  const color = colors[state.status];
  const since = formatTimeSince(sinceForState(state) ?? undefined);
  const tooltip = formatStatusTooltip(state, rule, state.metricName);

  return (
    <HStack gap="2" align="baseline">
      <HStack
        gap="1.5"
        px="1.5"
        py="0.5"
        borderRadius="sm"
        bg={`${color}1F`}
        display="inline-flex"
        title={tooltip ?? undefined}
        cursor={tooltip ? "help" : undefined}
      >
        <Box w="1.5" h="1.5" borderRadius="full" bg={color} />
        <Text
          fontSize="2xs"
          fontWeight="semibold"
          color={color}
          letterSpacing="0.04em"
        >
          {labels[state.status]}
        </Text>
      </HStack>
      {since ? (
        <Text fontSize="2xs" color="fg.subtle" fontFamily="mono">
          {since}
        </Text>
      ) : null}
    </HStack>
  );
}

function SortableHeader({
  label,
  column,
  activeKey,
  dir,
  onClick,
}: {
  label: string;
  column: SortKey;
  activeKey: SortKey;
  dir: SortDir;
  onClick: (key: SortKey) => void;
}) {
  const active = activeKey === column;
  const indicator = active ? (dir === "asc" ? " ↑" : " ↓") : "";

  return (
    <Table.ColumnHeader>
      <chakra.button
        type="button"
        onClick={() => onClick(column)}
        display="inline-flex"
        alignItems="center"
        gap="1"
        px="0"
        py="0"
        bg="transparent"
        border="none"
        cursor="pointer"
        color={active ? "fg.default" : "fg.muted"}
        fontWeight={active ? "semibold" : "inherit"}
        fontSize="inherit"
        textTransform="inherit"
        letterSpacing="inherit"
        _hover={{ color: "fg.default" }}
      >
        {label}
        <Text as="span" fontFamily="mono" minW="1ch">
          {indicator}
        </Text>
      </chakra.button>
    </Table.ColumnHeader>
  );
}

function SourceBadge({ source }: { source: "config" | "bleemeo" }) {
  const palette = SOURCE_COLORS[source];

  return (
    <Box
      as="span"
      display="inline-block"
      px="2"
      py="0.5"
      fontSize="2xs"
      fontWeight="semibold"
      letterSpacing="0.04em"
      textTransform="uppercase"
      borderRadius="sm"
      bg={palette.bg}
      color={palette.fg}
    >
      {source}
    </Box>
  );
}

function formatBound(
  value: number | null,
  color: string,
  metricName: string,
): ReactNode {
  if (value == null) {
    return (
      <Text as="span" color="fg.subtle">
        —
      </Text>
    );
  }

  return (
    <Text as="span" color={color}>
      {formatBoundValue(value, metricName)}
    </Text>
  );
}

function formatDelay(warnSec: number, critSec: number): string {
  if (warnSec === 0 && critSec === 0) return "immediate";

  return `warn ${warnSec}s / crit ${critSec}s`;
}
