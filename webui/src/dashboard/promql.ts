// Helpers for digesting prom matrix responses into shapes that
// Recharts and the KPI cards can consume directly.

import type { PromQLResponse, PromQLSeries } from "../api/types";

export type Sample = { t: number; v: number };

export function samplesOf(series: PromQLSeries | undefined): Sample[] {
  if (!series) return [];

  const out: Sample[] = [];

  for (const [t, raw] of series.values) {
    const v = parseFloat(raw);

    if (isFinite(v)) {
      out.push({ t, v });
    }
  }

  return out;
}

/**
 * seriesByLabel turns a multi-series PromQL response into a map keyed
 * by the value of the given label. Useful for charts whose number of
 * series is known only at query time (e.g. one line per filesystem
 * mountpoint or one line per network interface).
 */
export function seriesByLabel(
  response: PromQLResponse | null | undefined,
  labelName: string,
): Record<string, PromQLSeries> {
  const out: Record<string, PromQLSeries> = {};

  for (const s of response?.data?.result ?? []) {
    const value = s.metric[labelName];
    if (!value) continue;
    out[value] = s;
  }

  return out;
}

/**
 * sumSeries collapses several PromQL series into a single virtual one
 * whose value at every timestamp is the sum of all input values at
 * that timestamp. Used client-side to recompute "All interfaces" /
 * "All devices" totals when the dropdown is set to "All" so we can
 * keep the per-item query and avoid a second round-trip.
 *
 * Timestamps that don't appear in every series are kept; missing
 * contributions are treated as 0 (a series silently absent at a step
 * shouldn't make the total disappear).
 */
export function sumSeries(inputs: PromQLSeries[]): PromQLSeries | undefined {
  if (inputs.length === 0) return undefined;
  if (inputs.length === 1) return inputs[0];

  const totals = new Map<number, number>();

  for (const s of inputs) {
    for (const [t, raw] of s.values) {
      const v = parseFloat(raw);
      if (!isFinite(v)) continue;
      totals.set(t, (totals.get(t) ?? 0) + v);
    }
  }

  const sorted = Array.from(totals.entries()).sort((a, b) => a[0] - b[0]);

  return {
    metric: {},
    values: sorted.map(([t, v]) => [t, String(v)]),
  };
}

export function lastValue(response: PromQLResponse | null | undefined): number | null {
  const series = response?.data?.result?.[0];
  const samples = samplesOf(series);

  if (samples.length === 0) return null;

  return samples[samples.length - 1].v;
}

/**
 * Aligns multiple series on a common timestamp grid so Recharts can
 * render them as stacked or overlaid in a single dataset. The first
 * series defines the grid; later series are looked up by exact
 * timestamp match (the matrix already comes step-aligned from the
 * server, so this is normally trivial).
 */
export function alignSeries(
  named: Record<string, PromQLSeries | undefined>,
): Array<Record<string, number>> {
  const entries = Object.entries(named);
  if (entries.length === 0) return [];

  const samplesByName = new Map(entries.map(([k, s]) => [k, samplesOf(s)]));
  const baseSamples = samplesByName.get(entries[0][0]) ?? [];

  return baseSamples.map((b) => {
    const row: Record<string, number> = { t: b.t };

    for (const [name, samples] of samplesByName.entries()) {
      const m = samples.find((s) => s.t === b.t);
      if (m) row[name] = m.v;
    }

    return row;
  });
}
