// Helpers around the /data/thresholds endpoint. We fold the
// per-metric rules into a single lookup so consumers can ask
// "what's the warn/crit for cpu_used?" without iterating.

import { useMemo } from "react";

import { useFetch } from "../api/hooks";
import type {
  ThresholdState,
  ThresholdStatus,
  ThresholdsResponse,
} from "../api/types";

// STATUS_COLORS centralises the palette used across the dashboard
// (KPI cards / charts / banner) and the Informations table. The hex
// codes intentionally match Tailwind's red/amber/emerald/gray-400 so
// they line up visually with the rest of the UI.
//
// `bgTint` / `bgHover` are pre-mixed rgba forms for row backgrounds;
// only ok/warn/crit ever colour a row, so unknown only carries its
// solid swatch — keep the tints in sync with `solid` if you tweak the
// palette.
export const STATUS_COLORS = {
  ok: {
    solid: "#10B981",
    bgTint: "rgba(16, 185, 129, 0.06)",
    bgHover: "rgba(16, 185, 129, 0.12)",
  },
  warn: {
    solid: "#F59E0B",
    bgTint: "rgba(245, 158, 11, 0.06)",
    bgHover: "rgba(245, 158, 11, 0.12)",
  },
  crit: {
    solid: "#EF4444",
    bgTint: "rgba(239, 68, 68, 0.06)",
    bgHover: "rgba(239, 68, 68, 0.12)",
  },
  unknown: { solid: "#9CA3AF" },
} as const;

// SOURCE_COLORS palette used by the SourceBadge to tell config-driven
// from Bleemeo-driven threshold rules. The bleemeo entry uses the
// Bleemeo brand accent; if you ever lift the accent into a shared
// theme token (it's repeated elsewhere as a chart accent), point this
// at the token instead.
export const SOURCE_COLORS = {
  bleemeo: { bg: "rgba(79, 141, 245, 0.12)", fg: "#4F8DF5" },
  config: { bg: "rgba(148, 163, 184, 0.16)", fg: "fg.muted" },
} as const;

// toUiStatus maps the backend's "ok|warning|critical|unknown"
// vocabulary to the dashboard's shorter "ok|warn|crit|unknown" used
// by StatusBadge and chart helpers.
export function toUiStatus(
  s: ThresholdStatus | undefined,
): "ok" | "warn" | "crit" | "unknown" {
  if (s === "warning") return "warn";
  if (s === "critical") return "crit";
  if (s === "unknown") return "unknown";
  return "ok";
}

// Bounds resolved for a single metric, after merging the matching
// rule (if any) with optional fallback defaults. All four bounds are
// nullable because rules can leave any of them unset.
export type ThresholdBounds = {
  lowCritical: number | null;
  lowWarning: number | null;
  highWarning: number | null;
  highCritical: number | null;
  // Which source provided the matched rule. null when no rule
  // matched and the caller is falling back to defaults.
  source: "config" | "bleemeo" | null;
};

/**
 * useThresholds fetches /data/thresholds and exposes:
 *   - rules: the raw list of threshold rules (used by the table in
 *     the Informations page).
 *   - byMetric: a lookup map of bounds keyed by metric name. When
 *     several rules exist for the same metric name (e.g. one Bleemeo-
 *     instance-specific + one config-wide), Bleemeo wins — same
 *     precedence as the agent applies internally.
 *   - states: the raw list of current status states (one per
 *     threshold-tracked series).
 *   - stateByMetric: a lookup of the current state keyed by metric
 *     name. When several series share a metric name (e.g.
 *     disk_used_perc per mountpoint), the WORST state wins so the
 *     dashboard surfaces the unhappy mountpoint at a glance.
 *   - summary: aggregate counts ({ warning, critical }) of currently
 *     firing states across all series. 0/0 means everything is ok.
 */
export function useThresholds() {
  const res = useFetch<ThresholdsResponse>("/data/thresholds", 60_000);

  const byMetric = useMemo(() => {
    const out: Record<string, ThresholdBounds> = {};

    for (const r of res.data?.thresholds ?? []) {
      const existing = out[r.metricName];

      // Bleemeo overrides config (mirrors threshold.Registry.getThreshold).
      if (existing && existing.source === "bleemeo" && r.source === "config") {
        continue;
      }

      out[r.metricName] = {
        lowCritical: r.lowCritical,
        lowWarning: r.lowWarning,
        highWarning: r.highWarning,
        highCritical: r.highCritical,
        source: r.source,
      };
    }

    return out;
  }, [res.data]);

  const states = res.data?.states ?? [];

  const stateByMetric = useMemo(() => {
    const out: Record<string, ThresholdState> = {};
    for (const s of states) {
      const existing = out[s.metricName];
      if (!existing || statusRank(s.status) > statusRank(existing.status)) {
        out[s.metricName] = s;
      }
    }
    return out;
  }, [states]);

  const summary = useMemo(() => {
    let warning = 0;
    let critical = 0;
    for (const s of states) {
      if (s.status === "warning") warning++;
      else if (s.status === "critical") critical++;
    }
    return { warning, critical };
  }, [states]);

  return {
    rules: res.data?.thresholds ?? [],
    byMetric,
    states,
    stateByMetric,
    summary,
    loading: res.loading,
    error: res.error,
  };
}

// statusRank gives "worst" a higher numeric value so a max-fold picks
// it. Used to fold per-item states (e.g. one per mountpoint) into a
// single per-metric status, and to sort tables worst-first. `undefined`
// (no state at all) ranks below ok so untracked rows sink to the
// bottom of a worst-first sort.
export function statusRank(s: ThresholdStatus | undefined): number {
  switch (s) {
    case "critical":
      return 3;
    case "warning":
      return 2;
    case "unknown":
      return 1;
    case "ok":
    default:
      return 0;
  }
}

/**
 * formatTimeSince returns a short "12m ago" / "2h" duration label
 * from an ISO timestamp string. Returns null if the timestamp is
 * missing or invalid — caller can hide the hint in that case.
 */
export function formatTimeSince(iso: string | undefined): string | null {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  if (!isFinite(t) || t === 0) return null;

  const sec = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)}h`;
  return `${(sec / 86400).toFixed(1)}d`;
}

/**
 * sinceForState returns the relevant "since when" timestamp for a
 * status: criticalSince when critical, warningSince when warning,
 * null otherwise. Caller passes it to formatTimeSince to display.
 */
export function sinceForState(
  state: ThresholdState | undefined,
): string | null {
  if (!state) return null;
  if (state.status === "critical") return state.criticalSince ?? null;
  if (state.status === "warning") return state.warningSince ?? null;
  return null;
}

/**
 * unitSuffixFor returns a short display unit for well-known metric
 * names. Pragmatic: covers the metrics users actually threshold on
 * (CPU/RAM/disk/swap percent, response times) and falls back to no
 * suffix for the rest. Keeps the wire format simple — the agent
 * already tracks units internally, but exposing them all the way to
 * the UI would force a backend round-trip for a one-character gain.
 */
export function unitSuffixFor(metricName: string): string {
  if (metricName.endsWith("_perc")) return "%";
  if (metricName.endsWith("_seconds") || metricName === "probe_duration")
    return "s";
  return "";
}

/**
 * formatBoundValue formats a numeric threshold bound with its unit
 * suffix when known. Returns "—" for null/undefined so the UI can
 * render unset bounds uniformly.
 */
export function formatBoundValue(
  value: number | null | undefined,
  metricName: string,
): string {
  if (value == null) return "—";
  const suffix = unitSuffixFor(metricName);
  // Round to 2 decimals for non-integer values, drop trailing zeros.
  const display = Number.isInteger(value)
    ? String(value)
    : value.toFixed(2).replace(/\.?0+$/, "");
  return `${display}${suffix}`;
}

// BoundRails is the minimal shape formatStatusTooltip needs — just the
// four numeric rails. Accepting this rather than the full
// ThresholdBounds lets callers pass a ThresholdRule directly without a
// structural-typing accident, and keeps the helper independent from
// the `source` field which it doesn't read.
export type BoundRails = Pick<
  ThresholdBounds,
  "lowCritical" | "lowWarning" | "highWarning" | "highCritical"
>;

/**
 * formatStatusTooltip composes a hover hint summarising where the
 * current value sits relative to its thresholds. Used both on the
 * Informations table status pill and on dashboard KPI cards.
 *
 *  - warning/critical: "current 95.2%, threshold 90% (+5.2% over warn)"
 *  - ok with bounds:   "current 45%, high warn at 80% (35% headroom)"
 *  - no bounds / no value: null (caller hides the tooltip).
 */
export function formatStatusTooltip(
  state: ThresholdState | undefined,
  bounds: BoundRails,
  metricName: string,
): string | null {
  if (!state) return null;

  const value = state.lastValue;
  if (value == null || !Number.isFinite(value)) return null;

  const fmt = (v: number) => formatBoundValue(v, metricName);
  const currentPart = `current ${fmt(value)}`;

  if (state.status === "critical" || state.status === "warning") {
    const crossed = findCrossedBound(value, state.status, bounds);
    if (!crossed) return currentPart;

    const delta = Math.abs(value - crossed.bound);
    const sign = crossed.side === "over" ? "+" : "-";
    return `${currentPart}, threshold ${fmt(crossed.bound)} (${sign}${fmt(delta)} ${crossed.side} ${crossed.level})`;
  }

  if (state.status === "ok") {
    // Prefer the warning rail (closer to "starting to be a problem")
    // but fall back to critical so a rule that only sets crit bounds
    // still surfaces *some* context instead of a bare "current X".
    const nearest =
      nearestRail(value, bounds, "warn") ?? nearestRail(value, bounds, "crit");
    if (!nearest) return currentPart;
    const headroom = Math.abs(value - nearest.bound);
    const label = `${nearest.side === "over" ? "high" : "low"} ${nearest.level}`;
    return `${currentPart}, ${label} at ${fmt(nearest.bound)} (${fmt(headroom)} headroom)`;
  }

  return null;
}

type CrossedBound = {
  bound: number;
  side: "over" | "under";
  level: "warn" | "crit";
};

function findCrossedBound(
  value: number,
  status: "warning" | "critical",
  bounds: BoundRails,
): CrossedBound | null {
  if (status === "critical") {
    if (bounds.highCritical != null && value >= bounds.highCritical) {
      return { bound: bounds.highCritical, side: "over", level: "crit" };
    }
    if (bounds.lowCritical != null && value <= bounds.lowCritical) {
      return { bound: bounds.lowCritical, side: "under", level: "crit" };
    }
  }
  if (bounds.highWarning != null && value >= bounds.highWarning) {
    return { bound: bounds.highWarning, side: "over", level: "warn" };
  }
  if (bounds.lowWarning != null && value <= bounds.lowWarning) {
    return { bound: bounds.lowWarning, side: "under", level: "warn" };
  }
  return null;
}

function nearestRail(
  value: number,
  bounds: BoundRails,
  level: "warn" | "crit",
): { bound: number; side: "over" | "under"; level: "warn" | "crit" } | null {
  const high = level === "warn" ? bounds.highWarning : bounds.highCritical;
  const low = level === "warn" ? bounds.lowWarning : bounds.lowCritical;
  if (high != null && low != null) {
    // Equidistant ties prefer high — most percent KPIs only have a
    // ceiling so this reads naturally to users.
    return high - value <= value - low
      ? { bound: high, side: "over", level }
      : { bound: low, side: "under", level };
  }
  if (high != null) return { bound: high, side: "over", level };
  if (low != null) return { bound: low, side: "under", level };
  return null;
}

/**
 * statusFromBounds returns "ok" / "warn" / "crit" / "unknown" for a
 * numeric value against a bounds set. Mirrors the agent's logic:
 * critical (low or high) wins over warning, warning wins over ok.
 */
export function statusFromBounds(
  v: number | null | undefined,
  bounds: ThresholdBounds,
): "ok" | "warn" | "crit" | "unknown" {
  if (v == null || !isFinite(v)) return "unknown";

  if (bounds.lowCritical != null && v <= bounds.lowCritical) return "crit";
  if (bounds.highCritical != null && v >= bounds.highCritical) return "crit";
  if (bounds.lowWarning != null && v <= bounds.lowWarning) return "warn";
  if (bounds.highWarning != null && v >= bounds.highWarning) return "warn";

  return "ok";
}
