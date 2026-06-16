import { SimpleGrid } from "@chakra-ui/react";

import { usePromQLRange } from "../api/hooks";
import { formatBitsPerSec, formatNumber, formatPercent } from "./format";
import { KPICard } from "./KPICard";
import { lastValue } from "./promql";
import {
  formatStatusTooltip,
  formatTimeSince,
  sinceForState,
  statusFromBounds,
  useThresholds,
  type ThresholdBounds,
} from "./thresholds";
import type { ThresholdState } from "../api/types";

const SHORT_RANGE = 60; // seconds — the "now" snapshot the cards summarise
const SHORT_STEP = 10;

// Pragmatic fallbacks applied when the user hasn't configured a
// threshold for the standard percent KPIs — values mirror the
// defaults Bleemeo plans ship with.
const CPU_FALLBACK = { warn: 80, crit: 95 } as const;
const RAM_FALLBACK = { warn: 80, crit: 95 } as const;
const SWAP_FALLBACK = { warn: 50, crit: 80 } as const;

// Load fallback bounds in per-core units; scaled by `cores` at the
// call-site so warn fires at 0.8/core regardless of machine size.
const LOAD_PER_CORE_FALLBACK = { warn: 0.8, crit: 1.5 } as const;

// Raw fallback when we don't know the core count (load is on an
// uncalibrated scale at that point — assume a small box).
const LOAD_RAW_FALLBACK = { warn: 4, crit: 8 } as const;

// highOnlyBounds builds a ThresholdBounds with only the high warn/crit
// rails set; covers the common "alert when above X%" case used by
// every standard KPI.
function highOnlyBounds(fallback: {
  warn?: number;
  crit?: number;
}): ThresholdBounds {
  return {
    lowCritical: null,
    lowWarning: null,
    highWarning: fallback.warn ?? null,
    highCritical: fallback.crit ?? null,
    source: null,
  };
}

// statusHintFor builds the inline "WARNING for 12m" / "CRITICAL for 2h"
// hint shown on KPI cards. Returns undefined when the metric isn't
// currently in warn/crit or when the state's "since" timestamp is
// missing (typical the first few seconds after the threshold engine
// runs against a fresh metric).
function statusHintFor(state: ThresholdState | undefined): string | undefined {
  if (!state) return undefined;
  if (state.status !== "warning" && state.status !== "critical")
    return undefined;

  const since = formatTimeSince(sinceForState(state) ?? undefined);
  if (!since) return state.status.toUpperCase();

  return `${state.status.toUpperCase()} for ${since}`;
}

export function KPIRow({ cores }: { cores: number | undefined }) {
  const cpu = usePromQLRange("cpu_used", SHORT_RANGE, SHORT_STEP);
  const mem = usePromQLRange("mem_used_perc", SHORT_RANGE, SHORT_STEP);
  const swap = usePromQLRange("swap_used_perc", SHORT_RANGE, SHORT_STEP);
  const load = usePromQLRange("system_load1", SHORT_RANGE, SHORT_STEP);
  const netIn = usePromQLRange("sum(net_bits_recv)", SHORT_RANGE, SHORT_STEP);
  const netOut = usePromQLRange("sum(net_bits_sent)", SHORT_RANGE, SHORT_STEP);

  const { byMetric, stateByMetric } = useThresholds();

  const cpuV = lastValue(cpu.data);
  const memV = lastValue(mem.data);
  const swapV = lastValue(swap.data);
  const loadV = lastValue(load.data);
  const netInV = lastValue(netIn.data);
  const netOutV = lastValue(netOut.data);

  // Normalise load by core count so the warn/crit thresholds make sense
  // across different machines.
  const normalisedLoad = loadV != null && cores ? loadV / cores : loadV;

  const cpuBounds = byMetric.cpu_used ?? highOnlyBounds(CPU_FALLBACK);
  const memBounds = byMetric.mem_used_perc ?? highOnlyBounds(RAM_FALLBACK);
  const swapBounds = byMetric.swap_used_perc ?? highOnlyBounds(SWAP_FALLBACK);

  // system_load1 thresholds, when configured, target the raw load
  // value. Otherwise scale the per-core heuristic by cores so a
  // user-provided bound stays comparable to the fallback shape and
  // statusFromBounds can be the single evaluation path.
  const loadBounds =
    byMetric.system_load1 ??
    (cores != null
      ? highOnlyBounds({
          warn: cores * LOAD_PER_CORE_FALLBACK.warn,
          crit: cores * LOAD_PER_CORE_FALLBACK.crit,
        })
      : highOnlyBounds(LOAD_RAW_FALLBACK));
  const loadStatus = statusFromBounds(loadV, loadBounds);

  return (
    <SimpleGrid columns={{ base: 2, md: 3, xl: 6 }} gap="3">
      <KPICard
        label="CPU"
        value={formatPercent(cpuV)}
        fillRatio={cpuV == null ? undefined : cpuV / 100}
        status={statusFromBounds(cpuV, cpuBounds)}
        statusHint={statusHintFor(stateByMetric.cpu_used)}
        tooltip={
          formatStatusTooltip(stateByMetric.cpu_used, cpuBounds, "cpu_used") ??
          undefined
        }
        hint={cores ? `${cores} cores` : undefined}
      />
      <KPICard
        label="RAM"
        value={formatPercent(memV)}
        fillRatio={memV == null ? undefined : memV / 100}
        status={statusFromBounds(memV, memBounds)}
        statusHint={statusHintFor(stateByMetric.mem_used_perc)}
        tooltip={
          formatStatusTooltip(
            stateByMetric.mem_used_perc,
            memBounds,
            "mem_used_perc",
          ) ?? undefined
        }
      />
      <KPICard
        label="Swap"
        value={formatPercent(swapV)}
        fillRatio={swapV == null ? undefined : swapV / 100}
        status={statusFromBounds(swapV, swapBounds)}
        statusHint={statusHintFor(stateByMetric.swap_used_perc)}
        tooltip={
          formatStatusTooltip(
            stateByMetric.swap_used_perc,
            swapBounds,
            "swap_used_perc",
          ) ?? undefined
        }
      />
      <KPICard
        label="Load avg (1m)"
        value={formatNumber(loadV, 2)}
        fillRatio={
          normalisedLoad == null ? undefined : Math.min(1, normalisedLoad / 1.5)
        }
        status={loadStatus}
        statusHint={statusHintFor(stateByMetric.system_load1)}
        tooltip={
          formatStatusTooltip(
            stateByMetric.system_load1,
            loadBounds,
            "system_load1",
          ) ?? undefined
        }
        hint={
          cores
            ? `per core: ${formatNumber(normalisedLoad ?? 0, 2)}`
            : undefined
        }
      />
      <KPICard
        label="↓ Network"
        value={formatBitsPerSec(netInV)}
        status="unknown"
      />
      <KPICard
        label="↑ Network"
        value={formatBitsPerSec(netOutV)}
        status="unknown"
      />
    </SimpleGrid>
  );
}
