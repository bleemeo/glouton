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
