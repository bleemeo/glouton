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

export interface MetricPoints {
  metric: {
    __name__: string;
    instance: string;
  };
  values: [number, string][];
}

export interface MetricFetchResult {
  metric: Metric;
  values: [number, string][];
}

export const gaugesBarPrometheusLinux: GaugeBar[] = [
  {
    title: "CPU",
    metrics: [
      {
        query:
          '(1-sum(irate(node_cpu_seconds_total{mode="idle"}[1m]))/sum(irate(node_cpu_seconds_total[1m])))*100',
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: [
      {
        query:
          "(1-node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)*100",
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: [
      {
        query: "irate(node_disk_io_time_seconds_total[1m])*100",
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: [
      {
        query:
          '(1-node_filesystem_avail_bytes{mountpoint="/"}/node_filesystem_size_bytes{mountpoint="/"})*100',
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
];

export const gaugesBarPrometheusWindows = [
  {
    title: "CPU",
    metrics: [
      {
        query:
          '(1-sum(irate(windows_cpu_time_global{mode="idle"}[1m]))/sum(irate(windows_cpu_time_global[1m])))*100',
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: [
      {
        query:
          "(1-windows_memory_available_bytes/windows_cs_physical_memory_bytes)*100",
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: [
      {
        query: "100-irate(windows_logical_disk_idle_seconds_total[1m])*100",
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "C:",
    metrics: [
      {
        query:
          '(1-windows_logical_disk_free_bytes{volume="C:"}/windows_logical_disk_size_bytes{volume="C:"})*100',
      },
    ],
    unit: UNIT_PERCENTAGE,
  },
];

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
        legend: "Total",
        query: "process_total",
      },
      {
        legend: "Running",
        query: "process_status_running",
      },
      {
        legend: "Sleeping",
        query: "process_status_sleeping",
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
