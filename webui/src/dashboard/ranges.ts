// Time-range options offered by the dashboard. Listed shortest first.
//
// Step values target ~80-180 points per chart so Recharts stays
// responsive and the on-disk TSDB query stays cheap. They are advisory:
// Glouton's PromQL backend will return whatever it has at higher
// resolution if the step is smaller than the underlying scrape interval.

export type Range = {
  id: string;
  label: string;
  seconds: number;
  step: number;
};

export const RANGES: Range[] = [
  { id: "5m", label: "5m", seconds: 5 * 60, step: 10 },
  { id: "15m", label: "15m", seconds: 15 * 60, step: 10 },
  { id: "1h", label: "1h", seconds: 60 * 60, step: 30 },
  { id: "6h", label: "6h", seconds: 6 * 60 * 60, step: 120 },
  { id: "24h", label: "24h", seconds: 24 * 60 * 60, step: 600 },
  { id: "7d", label: "7d", seconds: 7 * 24 * 60 * 60, step: 60 * 30 },
  { id: "30d", label: "30d", seconds: 30 * 24 * 60 * 60, step: 60 * 60 * 2 },
];

export const DEFAULT_RANGE_ID = "1h";

/**
 * Returns true if a range is queryable given how far back the local
 * store actually goes. We require the available history to cover at
 * least 25% of the requested range — past that point the chart would
 * be mostly empty.
 */
export function isRangeAvailable(
  range: Range,
  oldestPointMs: number | undefined,
): boolean {
  if (!oldestPointMs) {
    return false;
  }

  const haveSeconds = (Date.now() - oldestPointMs) / 1000;

  return haveSeconds >= range.seconds * 0.25;
}
