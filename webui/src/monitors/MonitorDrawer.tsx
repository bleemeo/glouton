import {
  Box,
  Drawer,
  HStack,
  IconButton,
  Link as ChakraLink,
  Portal,
  SimpleGrid,
  Text,
  VStack,
} from "@chakra-ui/react";
import { useState } from "react";
import { LuExternalLink, LuX } from "react-icons/lu";

import { usePromQLRange, useStoreInfo } from "../api/hooks";
import type { Monitor } from "../api/types";
import { RangeSelector } from "../dashboard/RangeSelector";
import { DEFAULT_RANGE_ID, RANGES, type Range } from "../dashboard/ranges";
import {
  alignSeries,
  lastValue,
  samplesOf,
  seriesByLabel,
} from "../dashboard/promql";
import { MetricChart, type ChartSeries } from "../dashboard/MetricChart";
import { StatusBadge } from "../app/StatusBadge";
import {
  formatLatency,
  formatCertExpiry,
  probeSuccessToStatus,
} from "./format";
import { MonitorAvailability } from "./MonitorAvailability";
import { instanceSelector } from "./promql";
import { RecentFailures } from "./RecentFailures";

type Props = {
  monitor: Monitor | null;
  onClose: () => void;
};

export function MonitorDrawer({ monitor, onClose }: Props) {
  const open = monitor !== null;

  return (
    <Drawer.Root
      open={open}
      onOpenChange={(d) => {
        if (!d.open) onClose();
      }}
      size="xl"
      placement="end"
    >
      <Portal>
        <Drawer.Backdrop bg="blackAlpha.500" />
        <Drawer.Positioner>
          <Drawer.Content
            bg="surface.panel"
            borderLeftWidth="1px"
            borderColor="border.subtle"
          >
            {monitor ? (
              <MonitorDetail monitor={monitor} onClose={onClose} />
            ) : null}
          </Drawer.Content>
        </Drawer.Positioner>
      </Portal>
    </Drawer.Root>
  );
}

function MonitorDetail({
  monitor,
  onClose,
}: {
  monitor: Monitor;
  onClose: () => void;
}) {
  const storeInfo = useStoreInfo();
  const [range, setRange] = useState<Range>(
    () =>
      RANGES.find((r) => r.id === DEFAULT_RANGE_ID) ??
      RANGES.find((r) => r.id === "1h") ??
      RANGES[0],
  );

  const sel = instanceSelector(monitor.name);
  const probe = usePromQLRange(
    `probe_success${sel}`,
    range.seconds,
    range.step,
  );
  const lastSuccess = lastValue(probe.data);

  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle" pb="3">
        <HStack justify="space-between" align="start" gap="3">
          <VStack align="start" gap="2" minW="0" flex="1">
            <HStack gap="2">
              <StatusBadge
                status={probeSuccessToStatus(lastSuccess)}
                label={
                  lastSuccess == null
                    ? "Unknown"
                    : lastSuccess >= 1
                      ? "Up"
                      : "Down"
                }
              />
              <Text
                fontSize="xs"
                color="fg.muted"
                textTransform="uppercase"
                letterSpacing="0.06em"
              >
                {monitor.module}
              </Text>
              {monitor.source === "bleemeo" ? (
                <Box
                  px="1.5"
                  py="0.5"
                  fontSize="2xs"
                  fontWeight="semibold"
                  borderRadius="sm"
                  bg="rgba(79, 141, 245, 0.12)"
                  color="#4F8DF5"
                  letterSpacing="0.04em"
                  textTransform="uppercase"
                  title="Provisioned by Bleemeo Cloud"
                >
                  Bleemeo
                </Box>
              ) : null}
            </HStack>
            <Drawer.Title
              fontSize="lg"
              fontWeight="semibold"
              wordBreak="break-all"
            >
              {monitor.name}
            </Drawer.Title>
            <ChakraLink
              href={monitor.url}
              target="_blank"
              rel="noreferrer noopener"
              display="inline-flex"
              alignItems="center"
              gap="1.5"
              fontSize="xs"
              fontFamily="mono"
              color="fg.muted"
              _hover={{ color: "fg.default" }}
            >
              {monitor.url}
              <LuExternalLink />
            </ChakraLink>
          </VStack>
          <IconButton
            aria-label="Close"
            size="sm"
            variant="ghost"
            onClick={onClose}
          >
            <LuX />
          </IconButton>
        </HStack>
      </Drawer.Header>

      <Drawer.Body>
        <VStack align="stretch" gap="5" pt="2">
          <RangeSelector
            selectedId={range.id}
            onSelect={setRange}
            storeInfo={storeInfo.data}
          />

          <Section label="Availability">
            <MonitorAvailability name={monitor.name} range={range} />
          </Section>

          <RecentFailures name={monitor.name} range={range} />

          <ResponseTimeChart name={monitor.name} range={range} />

          {isHTTPMonitor(monitor) ? (
            <>
              <PhaseChart name={monitor.name} range={range} />
              <StatusCodeChart name={monitor.name} range={range} />
            </>
          ) : null}

          {isDNSMonitor(monitor) ? (
            <>
              <DNSLookupChart name={monitor.name} range={range} />
              <DNSRcodeChart name={monitor.name} range={range} />
            </>
          ) : null}

          {monitor.scheme === "https" || monitor.scheme === "ssl" ? (
            <CertificateSection name={monitor.name} />
          ) : null}
        </VStack>
      </Drawer.Body>
    </>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <VStack align="stretch" gap="2">
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        {label}
      </Text>
      {children}
    </VStack>
  );
}

function ResponseTimeChart({ name, range }: { name: string; range: Range }) {
  const sel = instanceSelector(name);
  const res = usePromQLRange(
    `probe_duration_seconds${sel}`,
    range.seconds,
    range.step,
  );
  const samples = samplesOf(res.data?.data?.result?.[0]);
  const data = samples.map((s) => ({ t: s.t, duration: s.v }));

  const series: ChartSeries[] = [
    { key: "duration", label: "Total", color: "#4F8DF5" },
  ];

  return (
    <MetricChart
      title="Response time"
      summary={
        samples.length > 0
          ? formatLatency(samples[samples.length - 1].v)
          : undefined
      }
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      loading={res.loading}
      error={res.error}
      variant="line"
      formatValue={(v) => formatLatency(v)}
    />
  );
}

// isHTTPMonitor / isDNSMonitor select the right detail charts. The
// blackbox config exposes both the `module` (string from the config
// file) and a `scheme` derived from the URL — we look at both so users
// who renamed their module still get the right view.
function isHTTPMonitor(m: { module: string; scheme: string }): boolean {
  return m.module === "http" || m.scheme === "http" || m.scheme === "https";
}

function isDNSMonitor(m: { module: string; scheme: string }): boolean {
  return m.module === "dns" || m.scheme === "dns";
}

const PHASE_ORDER = [
  "resolve",
  "connect",
  "tls",
  "processing",
  "transfer",
] as const;
const PHASE_COLORS: Record<string, string> = {
  resolve: "#8B5CF6",
  connect: "#4F8DF5",
  tls: "#10B981",
  processing: "#F59E0B",
  transfer: "#EF4444",
};

function PhaseChart({ name, range }: { name: string; range: Range }) {
  const sel = instanceSelector(name);
  const res = usePromQLRange(
    `probe_http_duration_seconds${sel}`,
    range.seconds,
    range.step,
  );
  const byPhase = seriesByLabel(res.data, "phase");
  const data = alignSeries(
    Object.fromEntries(PHASE_ORDER.map((p) => [p, byPhase[p]])),
  );

  const series: ChartSeries[] = PHASE_ORDER.filter((p) => byPhase[p]).map(
    (p) => ({
      key: p,
      label: p,
      color: PHASE_COLORS[p],
      stack: "phase",
    }),
  );

  return (
    <MetricChart
      title="HTTP phases"
      summary="Time spent in each phase, stacked"
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      loading={res.loading}
      error={res.error}
      variant="area"
      formatValue={(v) => formatLatency(v)}
    />
  );
}

function StatusCodeChart({ name, range }: { name: string; range: Range }) {
  const sel = instanceSelector(name);
  const res = usePromQLRange(
    `probe_http_status_code${sel}`,
    range.seconds,
    range.step,
  );
  const samples = samplesOf(res.data?.data?.result?.[0]);
  const data = samples.map((s) => ({ t: s.t, status: s.v }));

  const series: ChartSeries[] = [
    { key: "status", label: "Status code", color: "#10B981" },
  ];

  return (
    <MetricChart
      title="HTTP status code"
      summary={
        samples.length > 0
          ? String(Math.round(samples[samples.length - 1].v))
          : undefined
      }
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      yDomain={[0, "auto"]}
      loading={res.loading}
      error={res.error}
      variant="line"
      formatValue={(v) => String(Math.round(v))}
    />
  );
}

function DNSLookupChart({ name, range }: { name: string; range: Range }) {
  const sel = instanceSelector(name);
  const res = usePromQLRange(
    `probe_dns_lookup_time_seconds${sel}`,
    range.seconds,
    range.step,
  );
  const samples = samplesOf(res.data?.data?.result?.[0]);
  const data = samples.map((s) => ({ t: s.t, lookup: s.v }));

  const series: ChartSeries[] = [
    { key: "lookup", label: "Lookup time", color: "#8B5CF6" },
  ];

  return (
    <MetricChart
      title="DNS lookup time"
      summary={
        samples.length > 0
          ? formatLatency(samples[samples.length - 1].v)
          : undefined
      }
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      loading={res.loading}
      error={res.error}
      variant="line"
      formatValue={(v) => formatLatency(v)}
    />
  );
}

// DNS rcodes: 0 = NOERROR, 1 = FORMERR, 2 = SERVFAIL, 3 = NXDOMAIN,
// 4 = NOTIMP, 5 = REFUSED. Anything > 0 means the answer was not
// authoritative-success — we surface the numeric value and let the
// reader resolve it (full mapping in the tooltip).
const DNS_RCODE_LABELS: Record<number, string> = {
  0: "NOERROR",
  1: "FORMERR",
  2: "SERVFAIL",
  3: "NXDOMAIN",
  4: "NOTIMP",
  5: "REFUSED",
};

function DNSRcodeChart({ name, range }: { name: string; range: Range }) {
  const sel = instanceSelector(name);
  const res = usePromQLRange(
    `probe_dns_rcode${sel}`,
    range.seconds,
    range.step,
  );
  const samples = samplesOf(res.data?.data?.result?.[0]);
  const data = samples.map((s) => ({ t: s.t, rcode: s.v }));

  const series: ChartSeries[] = [
    { key: "rcode", label: "Response code", color: "#10B981" },
  ];

  const last =
    samples.length > 0 ? Math.round(samples[samples.length - 1].v) : null;
  const summary =
    last == null
      ? undefined
      : `${last} (${DNS_RCODE_LABELS[last] ?? "unknown"})`;

  return (
    <MetricChart
      title="DNS rcode"
      summary={summary}
      data={data}
      series={series}
      rangeSeconds={range.seconds}
      yDomain={[0, "auto"]}
      loading={res.loading}
      error={res.error}
      variant="line"
      formatValue={(v) => {
        const r = Math.round(v);
        return DNS_RCODE_LABELS[r]
          ? `${r} (${DNS_RCODE_LABELS[r]})`
          : String(r);
      }}
    />
  );
}

function CertificateSection({ name }: { name: string }) {
  const sel = instanceSelector(name);
  // The cert metrics are essentially static; query a short window with
  // a small step so we get the freshest value without spamming.
  const expiry = usePromQLRange(
    `probe_ssl_last_chain_expiry_timestamp_seconds${sel}`,
    60 * 60,
    60 * 5,
  );
  const valid = usePromQLRange(
    `probe_ssl_validation_success${sel}`,
    60 * 60,
    60 * 5,
  );
  const lifespan = usePromQLRange(
    `probe_ssl_leaf_certificate_lifespan${sel}`,
    60 * 60,
    60 * 5,
  );

  const expiryTs = lastValue(expiry.data);
  const validVal = lastValue(valid.data);
  const lifespanSec = lastValue(lifespan.data);

  const certInfo = formatCertExpiry(expiryTs);
  const expiryDate =
    expiryTs && expiryTs > 0 ? new Date(expiryTs * 1000).toLocaleString() : "—";
  const validLabel =
    validVal == null
      ? "Unknown"
      : validVal >= 1
        ? "Trusted chain"
        : "Untrusted chain";
  const validColor =
    validVal == null ? "fg.muted" : validVal >= 1 ? "#10B981" : "#EF4444";
  const lifespanLabel =
    lifespanSec && lifespanSec > 0
      ? `${Math.round(lifespanSec / 86_400)} days`
      : "—";

  return (
    <Section label="TLS certificate">
      <SimpleGrid columns={{ base: 1, sm: 3 }} gap="3">
        <CertField
          label="Expires"
          value={expiryDate}
          hint={certInfo.label}
          level={certInfo.level}
        />
        <CertField
          label="Validation"
          value={validLabel}
          valueColor={validColor}
        />
        <CertField label="Leaf lifespan" value={lifespanLabel} />
      </SimpleGrid>
    </Section>
  );
}

function CertField({
  label,
  value,
  hint,
  level,
  valueColor,
}: {
  label: string;
  value: string;
  hint?: string;
  level?: "ok" | "warn" | "crit";
  valueColor?: string;
}) {
  const accent =
    valueColor ??
    (level === "crit"
      ? "#EF4444"
      : level === "warn"
        ? "#F59E0B"
        : "fg.default");

  return (
    <Box
      bg="surface.subtle"
      borderRadius="md"
      borderWidth="1px"
      borderColor="border.subtle"
      p="3"
    >
      <VStack align="start" gap="0.5">
        <Text
          fontSize="xs"
          color="fg.muted"
          textTransform="uppercase"
          letterSpacing="0.06em"
        >
          {label}
        </Text>
        <Text
          fontSize="sm"
          fontFamily="mono"
          fontWeight="medium"
          color={accent}
        >
          {value}
        </Text>
        {hint ? (
          <Text fontSize="xs" color="fg.subtle">
            {hint}
          </Text>
        ) : null}
      </VStack>
    </Box>
  );
}
