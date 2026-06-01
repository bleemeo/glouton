// Small formatters used by the monitors tab. Kept separate from the
// dashboard's format.ts to avoid bloating it with probe-specific
// helpers that nobody else needs.

export function formatLatency(seconds: number | null | undefined): string {
  if (seconds == null || !isFinite(seconds)) return "—";

  if (seconds < 1) {
    return `${Math.round(seconds * 1000)} ms`;
  }

  return `${seconds.toFixed(2)} s`;
}

// formatCertExpiry returns a human-friendly "expires in 47d" style
// label from a unix timestamp (seconds). The blackbox exporter emits
// probe_ssl_last_chain_expiry_timestamp_seconds as the absolute
// expiry time; we subtract "now" client-side so the label updates
// even without a refetch.
export function formatCertExpiry(unixSec: number | null | undefined): {
  label: string;
  level: "ok" | "warn" | "crit";
} {
  if (unixSec == null || !isFinite(unixSec) || unixSec <= 0) {
    return { label: "—", level: "ok" };
  }

  const remainingSec = unixSec - Date.now() / 1000;

  if (remainingSec < 0) {
    return { label: "expired", level: "crit" };
  }

  const days = remainingSec / 86_400;

  if (days < 1) {
    return { label: `${Math.round(remainingSec / 3600)}h left`, level: "crit" };
  }

  if (days < 14) {
    return { label: `${Math.round(days)}d left`, level: "crit" };
  }

  if (days < 30) {
    return { label: `${Math.round(days)}d left`, level: "warn" };
  }

  return { label: `${Math.round(days)}d left`, level: "ok" };
}

export function probeSuccessToStatus(
  v: number | null | undefined,
): "ok" | "crit" | "unknown" {
  if (v == null || !isFinite(v)) return "unknown";

  return v >= 1 ? "ok" : "crit";
}
