import { UNIT_PERCENTAGE, UNIT_FLOAT, UNIT_INT } from "../utils";

export interface Metric {
  query: string;
  color?: string;
  icon?: { name: string; color: string };
  legend?: string;
}

export interface GaugeBar {
  title: string;
  type?: string;
  metrics: Metric[];
  unit: number;
  mountpoint?: string;
}

export interface NumberMetric {
  title: string;
  metrics: Metric[];
  submetrics?: NumberMetric[];
  unit: number;
}

export interface MetricFetchResult {
  metric: Metric;
  values: [number, string][];
}

export const gaugesBarBLEEMEO: GaugeBar[] = [
  {
    title: "CPU",
    metrics: [{ query: "cpu_used" }],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: [{ query: "mem_used_perc" }],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: [{ query: "io_utilization" }],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: [{ query: "disk_used_perc" }],
    mountpoint: "/",
    unit: UNIT_PERCENTAGE,
  },
];

export const numberMetricsBLEEMEO: NumberMetric[] = [
  {
    title: "Network",
    metrics: [
      {
        legend: "Received",
        query: "sum(net_bits_recv)",
        icon: { name: "arrow-down", color: "green" },
      },
      {
        legend: "Sent",
        query: "sum(net_bits_sent)",
        icon: { name: "arrow-up", color: "red" },
      },
    ],
    unit: UNIT_FLOAT,
  },
  {
    title: "Processes",
    metrics: [
      {
        legend: "Running",
        query: "process_status_running",
        icon: { name: "process-running", color: "green" },
      },
      {
        legend: "Sleeping",
        query: "process_status_sleeping",
        icon: { name: "process-sleeping", color: "blue" },
      },
    ],
    unit: UNIT_INT,
  },
  {
    title: "System Load",
    metrics: [{ query: "system_load1" }],
    unit: UNIT_FLOAT,
  },
];
