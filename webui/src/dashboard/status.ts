// Default thresholds used when the user has not configured any in
// glouton.conf. The local UI cannot read user thresholds (they live
// inside the agent), so we apply pragmatic defaults that match the
// values bundled with Bleemeo plans.

import type { Status } from "../app/StatusBadge";

export type Thresholds = {
  warn?: number;
  crit?: number;
};

export const DEFAULT_THRESHOLDS: Record<string, Thresholds> = {
  cpu: { warn: 80, crit: 95 },
  ram: { warn: 80, crit: 95 },
  swap: { warn: 50, crit: 80 },
  disk: { warn: 85, crit: 95 },
  load: { warn: 4, crit: 8 }, // load relative to # of cores; we'll normalize at the call-site
};

export function statusFromValue(value: number | null, t: Thresholds): Status {
  if (value == null || !isFinite(value)) return "unknown";

  if (t.crit != null && value >= t.crit) return "crit";

  if (t.warn != null && value >= t.warn) return "warn";

  return "ok";
}
