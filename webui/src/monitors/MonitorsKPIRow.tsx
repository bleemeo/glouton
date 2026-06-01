import { SimpleGrid } from "@chakra-ui/react";
import { useMemo } from "react";

import { usePromQLRange } from "../api/hooks";
import type { Monitor } from "../api/types";
import { KPICard } from "../dashboard/KPICard";
import { samplesOf, seriesByLabel } from "../dashboard/promql";

const PROBE_RANGE_SECONDS = 5 * 60;
const PROBE_STEP_SECONDS = 30;

const UPTIME_RANGE_SECONDS = 5 * 60;
const UPTIME_STEP_SECONDS = 60;

type Props = {
  monitors: Monitor[];
};

/**
 * MonitorsKPIRow summarises the whole monitors set in three cards:
 *   - Up / total — current state, count of monitors with probe_success==1
 *   - Avg uptime 24h — mean of avg_over_time(probe_success[24h]) across
 *     all monitors
 *   - Currently down — count of monitors with probe_success!=1 (or no
 *     sample yet), with a worst-name hint to point at the right card
 */
export function MonitorsKPIRow({ monitors }: Props) {
  // probe_success at the freshest timestamp for every monitor.
  const probe = usePromQLRange(
    monitors.length > 0 ? "probe_success" : null,
    PROBE_RANGE_SECONDS,
    PROBE_STEP_SECONDS,
  );

  // Per-monitor 24h uptime ratio. avg_over_time(probe_success[24h]) is
  // a gauge in [0, 1]; we average across all monitors below.
  const uptime = usePromQLRange(
    monitors.length > 0 ? "avg_over_time(probe_success[24h])" : null,
    UPTIME_RANGE_SECONDS,
    UPTIME_STEP_SECONDS,
  );

  const { upCount, downNames } = useMemo(() => {
    const byInstance = seriesByLabel(probe.data, "instance");

    let up = 0;
    const down: string[] = [];

    for (const m of monitors) {
      const samples = samplesOf(byInstance[m.name]);
      const v = samples.length > 0 ? samples[samples.length - 1].v : null;
      if (v != null && v >= 1) {
        up++;
      } else {
        down.push(m.name);
      }
    }

    return { upCount: up, downNames: down };
  }, [monitors, probe.data]);

  const avgUptimePct = useMemo(() => {
    if (monitors.length === 0) return null;

    const byInstance = seriesByLabel(uptime.data, "instance");
    let sum = 0;
    let count = 0;

    for (const m of monitors) {
      const samples = samplesOf(byInstance[m.name]);
      if (samples.length === 0) continue;
      sum += samples[samples.length - 1].v * 100;
      count++;
    }

    return count > 0 ? sum / count : null;
  }, [monitors, uptime.data]);

  const total = monitors.length;
  const downCount = downNames.length;

  const upStatus = downCount === 0 ? "ok" : "crit";
  const uptimeStatus =
    avgUptimePct == null
      ? "unknown"
      : avgUptimePct >= 99
        ? "ok"
        : avgUptimePct >= 95
          ? "warn"
          : "crit";
  const downStatus = downCount === 0 ? "ok" : downCount === 1 ? "warn" : "crit";

  return (
    <SimpleGrid columns={{ base: 1, sm: 3 }} gap="3">
      <KPICard
        label="Up"
        value={total === 0 ? "—" : `${upCount} / ${total}`}
        status={upStatus}
        fillRatio={total === 0 ? undefined : upCount / total}
      />
      <KPICard
        label="Avg uptime 24h"
        value={avgUptimePct == null ? "—" : `${avgUptimePct.toFixed(2)}%`}
        status={uptimeStatus}
        fillRatio={avgUptimePct == null ? undefined : avgUptimePct / 100}
      />
      <KPICard
        label="Currently down"
        value={String(downCount)}
        status={downStatus}
        hint={
          downCount === 0
            ? "All monitors reporting up"
            : downNames.slice(0, 3).join(", ") +
              (downNames.length > 3 ? ` +${downNames.length - 3}` : "")
        }
      />
    </SimpleGrid>
  );
}
