import { useMemo } from "react";

import { usePromQLRange } from "../api/hooks";
import type { Monitor, PromQLSeries } from "../api/types";
import { samplesOf, seriesByLabel, type Sample } from "../dashboard/promql";

// All queries that the monitors page derives from. Each one is fetched
// WITHOUT an instance filter so a single round-trip covers every
// monitor; downstream consumers slice by name. Without this batching
// each card fired its own 6 queries — N×6 round-trips per mount.
const PROBE_RANGE_SECONDS = 24 * 60 * 60;
const PROBE_STEP_SECONDS = 5 * 60;

const SHORT_RANGE_SECONDS = 5 * 60;
const SHORT_STEP_SECONDS = 30;

const CERT_RANGE_SECONDS = 60 * 60;
const CERT_STEP_SECONDS = 5 * 60;

export type MonitorStatus = "ok" | "down" | "unknown";

export type MonitorState = {
  monitor: Monitor;
  status: MonitorStatus;
  probeSamples: Sample[];
  uptimePct: number | null;
  p95Sec: number | null;
  httpStatusCode: number | null;
  tlsError: boolean;
  dnsOk: boolean | null;
  certExpiryUnix: number | null;
  lastCheckSec: number | null;
};

/**
 * useMonitorStates fans out one PromQL query per metric (with no
 * instance selector) and joins the resulting matrices back per
 * monitor. The returned `states` array matches the input `monitors`
 * order; `loading` flips to false as soon as the probe_success query
 * has answered (we don't gate on the rest because they only enrich).
 */
export function useMonitorStates(monitors: Monitor[]) {
  const hasMonitors = monitors.length > 0;

  const probe = usePromQLRange(
    hasMonitors ? "probe_success" : null,
    PROBE_RANGE_SECONDS,
    PROBE_STEP_SECONDS,
  );
  const latencyP95 = usePromQLRange(
    hasMonitors
      ? "quantile_over_time(0.95, probe_duration_seconds[24h])"
      : null,
    SHORT_RANGE_SECONDS,
    60,
  );
  const httpStatus = usePromQLRange(
    hasMonitors ? "probe_http_status_code" : null,
    SHORT_RANGE_SECONDS,
    SHORT_STEP_SECONDS,
  );
  const tlsError = usePromQLRange(
    hasMonitors ? "probe_failed_due_to_tls_error" : null,
    SHORT_RANGE_SECONDS,
    SHORT_STEP_SECONDS,
  );
  const dnsQuery = usePromQLRange(
    hasMonitors ? "probe_dns_query_succeeded" : null,
    SHORT_RANGE_SECONDS,
    SHORT_STEP_SECONDS,
  );
  const certExpiry = usePromQLRange(
    hasMonitors ? "probe_ssl_last_chain_expiry_timestamp_seconds" : null,
    CERT_RANGE_SECONDS,
    CERT_STEP_SECONDS,
  );

  const states: MonitorState[] = useMemo(() => {
    const probeBy = seriesByLabel(probe.data, "instance");
    const p95By = seriesByLabel(latencyP95.data, "instance");
    const httpBy = seriesByLabel(httpStatus.data, "instance");
    const tlsBy = seriesByLabel(tlsError.data, "instance");
    const dnsBy = seriesByLabel(dnsQuery.data, "instance");
    const certBy = seriesByLabel(certExpiry.data, "instance");

    return monitors.map((m) => {
      const probeSamples = samplesOf(probeBy[m.name]);
      const current =
        probeSamples.length > 0
          ? probeSamples[probeSamples.length - 1].v
          : null;
      const status: MonitorStatus =
        current == null ? "unknown" : current >= 1 ? "ok" : "down";

      const uptimePct =
        probeSamples.length === 0
          ? null
          : (probeSamples.filter((s) => s.v >= 1).length /
              probeSamples.length) *
            100;

      const lastCheckSec =
        probeSamples.length > 0
          ? probeSamples[probeSamples.length - 1].t
          : null;

      return {
        monitor: m,
        status,
        probeSamples,
        uptimePct,
        p95Sec: latestValue(p95By[m.name]),
        httpStatusCode: latestValue(httpBy[m.name]),
        tlsError: (latestValue(tlsBy[m.name]) ?? 0) >= 1,
        dnsOk: dnsOkValue(dnsBy[m.name]),
        certExpiryUnix:
          m.scheme === "https" ? latestValue(certBy[m.name]) : null,
        lastCheckSec,
      };
    });
  }, [
    monitors,
    probe.data,
    latencyP95.data,
    httpStatus.data,
    tlsError.data,
    dnsQuery.data,
    certExpiry.data,
  ]);

  return {
    states,
    loading: probe.loading,
    error: probe.error,
  };
}

function latestValue(series: PromQLSeries | undefined): number | null {
  if (!series || series.values.length === 0) return null;
  const samples = samplesOf(series);
  return samples.length > 0 ? samples[samples.length - 1].v : null;
}

function dnsOkValue(series: PromQLSeries | undefined): boolean | null {
  const v = latestValue(series);
  if (v == null) return null;
  return v >= 1;
}
