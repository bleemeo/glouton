// Number/byte/percent formatting helpers used across KPI cards and
// charts. Kept dependency-free so the bundle stays small.

const PERCENT = (() => {
  const f = new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  });

  return (v: number) => `${f.format(v)}%`;
})();

const SHORT = (() => {
  const f = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 2,
  });

  return (v: number) => f.format(v);
})();

export function formatPercent(v: number | null | undefined): string {
  if (v == null || !isFinite(v)) return "—";

  return PERCENT(v);
}

const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

export function formatBytes(v: number | null | undefined): string {
  if (v == null || !isFinite(v)) return "—";

  const sign = v < 0 ? "-" : "";
  let abs = Math.abs(v);
  let i = 0;

  while (abs >= 1024 && i < BYTE_UNITS.length - 1) {
    abs /= 1024;
    i += 1;
  }

  return `${sign}${SHORT(abs)} ${BYTE_UNITS[i]}`;
}

// formatBytesFromKiB formats a byte count expressed in KiB. The agent
// exposes process.memory_rss and topinfo.Memory.* in KiB (gopsutil's
// raw bytes are divided by 1024 in facts/process_unix.go and friends),
// so callers reading those fields must multiply by 1024 before
// formatting — this helper does that in one place.
export function formatBytesFromKiB(v: number | null | undefined): string {
  if (v == null || !isFinite(v)) return "—";

  return formatBytes(v * 1024);
}

export function formatBitsPerSec(v: number | null | undefined): string {
  if (v == null || !isFinite(v)) return "—";

  const sign = v < 0 ? "-" : "";
  let abs = Math.abs(v);
  const units = ["bps", "Kbps", "Mbps", "Gbps", "Tbps"];
  let i = 0;

  while (abs >= 1000 && i < units.length - 1) {
    abs /= 1000;
    i += 1;
  }

  return `${sign}${SHORT(abs)} ${units[i]}`;
}

export function formatNumber(v: number | null | undefined, digits = 2): string {
  if (v == null || !isFinite(v)) return "—";

  return v.toFixed(digits);
}

export function formatTickTime(
  unixSeconds: number,
  rangeSeconds: number,
): string {
  const d = new Date(unixSeconds * 1_000);

  if (rangeSeconds <= 60 * 60) {
    // ≤ 1h → HH:MM:SS
    return d.toLocaleTimeString(undefined, { hour12: false });
  }

  if (rangeSeconds <= 24 * 60 * 60) {
    // ≤ 24h → HH:MM
    return d.toLocaleTimeString(undefined, {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  // > 24h → mm/dd HH:MM
  return d.toLocaleString(undefined, {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
