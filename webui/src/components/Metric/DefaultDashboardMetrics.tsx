import { chartTypes, UNIT_PERCENTAGE, UNIT_BYTE, UNIT_NUMBER } from "../utils";

interface Metric {
  query: string;
  color?: string;
  legend?: string;
}

export interface GaugeBar {
  title: string;
  type?: string;
  metrics: Metric[];
  unit: number;
  mountpoint?: string;
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

export const widgetsBLEEMEO: GaugeBar[] = [
  {
    title: "Processor Usage",
    type: chartTypes[1],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "cpu_steal",
        color: "#c49c94",
        legend: "steal",
      },
      {
        query: "cpu_softirq",
        color: "#f7b6d2",
        legend: "softirq",
      },
      {
        query: "cpu_interrupt",
        color: "#c5b0d5",
        legend: "interrupt",
      },
      {
        query: "cpu_system",
        color: "#ff7f0e",
        legend: "system",
      },
      {
        query: "cpu_user",
        color: "#aec7e8",
        legend: "user",
      },
      {
        query: "cpu_nice",
        color: "#9edae5",
        legend: "nice",
      },
      {
        query: "cpu_wait",
        color: "#d62728",
        legend: "wait",
      },
      {
        query: "cpu_idle",
        color: "#98df8a",
        legend: "idle",
      },
    ],
  },
  {
    title: "Memory Usage",
    type: chartTypes[1],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "mem_used",
        color: "#aec7e8",
        legend: "used",
      },
      {
        query: "mem_buffered",
        color: "#c7c7c7",
        legend: "buffered",
      },
      {
        query: "mem_cached",
        color: "#dbdb8d",
        legend: "cached",
      },
      {
        query: "mem_free",
        color: "#98df8a",
        legend: "free",
      },
    ],
  },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "io_utilization",
        legend: "{{ item }}",
      },
    ],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "io_read_bytes",
        legend: "{{ item }}",
      },
    ],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "io_write_bytes",
        legend: "{{ item }}",
      },
    ],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "io_reads",
        legend: "{{ item }}",
      },
    ],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "io_writes",
        legend: "{{ item }}",
      },
    ],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "net_packets_recv",
        legend: "received from {{ item }}",
      },
      {
        query: "net_packets_sent",
        legend: "sent from {{ item }}",
      },
    ],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "net_err_in",
        legend: "received from {{ item }}",
      },
      {
        query: "net_err_out",
        legend: "sent from {{ item }}",
      },
    ],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "disk_used_perc",
        legend: "{{ item }}",
      },
    ],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "swap_used_perc",
        legend: "usage",
      },
    ],
  },
];

export const widgetsPrometheusLinux = [
  {
    title: "Processor Usage",
    type: chartTypes[1],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="steal"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#c49c94",
        legend: "steal",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="softirq"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#f7b6d2",
        legend: "softirq",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="irq"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#c5b0d5",
        legend: "interrupt",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="system"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#ff7f0e",
        legend: "system",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="user"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#aec7e8",
        legend: "user",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="nice"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#9edae5",
        legend: "nice",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="iowait"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#d62728",
        legend: "wait",
      },
      {
        query:
          'sum(irate(node_cpu_seconds_total{mode="idle"}[1m]))/sum(irate(node_cpu_seconds_total[1m]))*100',
        color: "#98df8a",
        legend: "idle",
      },
    ],
  },
  {
    title: "Memory Usage",
    type: chartTypes[1],
    unit: UNIT_BYTE,
    metrics: [
      {
        query:
          "node_memory_MemTotal_bytes - node_memory_MemFree_bytes -  node_memory_Cached_bytes - node_memory_Buffers_bytes",
        color: "#aec7e8",
        legend: "used",
      },
      {
        query: "node_memory_Buffers_bytes",
        color: "#c7c7c7",
        legend: "buffered",
      },
      {
        query: "node_memory_Cached_bytes",
        color: "#dbdb8d",
        legend: "cached",
      },
      {
        query: "node_memory_MemFree_bytes",
        color: "#98df8a",
        legend: "free",
      },
    ],
  },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "irate(node_disk_io_time_seconds_total[1m])*100",
        legend: "{{ device }}",
      },
    ],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "irate(node_disk_read_bytes_total[1m])",
        legend: "{{ device }}",
      },
    ],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "irate(node_disk_written_bytes_total[1m])",
        legend: "{{ device }}",
      },
    ],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(node_disk_reads_completed_total[1m])",
        legend: "{{ device }}",
      },
    ],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(node_disk_writes_completed_total[1m])",
        legend: "{{ device }}",
      },
    ],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(node_network_receive_bytes_total[1m])",
        legend: "received from {{ device }}",
      },
      {
        query: "irate(node_network_transmit_bytes_total[1m])",
        legend: "sent from {{ device }}",
      },
    ],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(node_network_receive_errs_total[1m])",
        legend: "received from {{ device }}",
      },
      {
        query: "irate(node_network_transmit_errs_total[1m])",
        legend: "sent from {{ device }}",
      },
    ],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "(1-node_filesystem_avail_bytes/node_filesystem_size_bytes)*100",
        legend: "{{ mountpoint }}",
      },
    ],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "(1-node_memory_SwapFree_bytes/node_memory_SwapTotal_bytes)*100",
        legend: "usage",
      },
    ],
  },
];

export const widgetsPrometheusWindows = [
  {
    title: "Processor Usage",
    type: chartTypes[1],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query:
          'sum(irate(windows_cpu_time_global{mode="dpc"}[1m]))/sum(irate(windows_cpu_time_global[1m]))*100',
        color: "#f7b6d2",
        legend: "dpc",
      },
      {
        query:
          'sum(irate(windows_cpu_time_global{mode="interrupt"}[1m]))/sum(irate(windows_cpu_time_global[1m]))*100',
        color: "#c5b0d5",
        legend: "interrupt",
      },
      {
        query:
          'sum(irate(windows_cpu_time_global{mode="privileged"}[1m]))/sum(irate(windows_cpu_time_global[1m]))*100',
        color: "#ff7f0e",
        legend: "privileged",
      },
      {
        query:
          'sum(irate(windows_cpu_time_global{mode="user"}[1m]))/sum(irate(windows_cpu_time_global[1m]))*100',
        color: "#aec7e8",
        legend: "user",
      },
      {
        query:
          'sum(irate(windows_cpu_time_global{mode="idle"}[1m]))/sum(irate(windows_cpu_time_global[1m]))*100',
        color: "#98df8a",
        legend: "idle",
      },
    ],
  },
  {
    title: "Memory Usage",
    type: chartTypes[1],
    unit: UNIT_BYTE,
    metrics: [
      {
        query:
          "windows_cs_physical_memory_bytes - windows_memory_available_bytes",
        color: "#aec7e8",
        legend: "used",
      },
      {
        query: "windows_memory_standby_cache_bytes",
        color: "#dbdb8d",
        legend: "cached",
      },
      {
        query:
          "windows_memory_available_bytes - windows_memory_standby_cache_bytes",
        color: "#98df8a",
        legend: "free",
      },
    ],
  },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query: "100-irate(windows_logical_disk_idle_seconds_total[1m])*100",
        legend: "{{ volume }}",
      },
    ],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "irate(windows_logical_disk_read_bytes_total[1m])",
        legend: "{{ volume }}",
      },
    ],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: [
      {
        query: "irate(windows_logical_disk_write_bytes_total[1m])",
        legend: "{{ volume }}",
      },
    ],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(windows_logical_disk_reads_total[1m])",
        legend: "{{ volume }}",
      },
    ],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(windows_logical_disk_writes_total[1m])",
        legend: "{{ volume }}",
      },
    ],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(windows_net_packets_received_total[1m])",
        legend: "received from {{ nic }}",
      },
      {
        query: "irate(windows_net_packets_sent_total[1m])",
        legend: "sent from {{ nic }}",
      },
    ],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      {
        query: "irate(windows_net_packets_received_errors[1m])",
        legend: "received from {{ nic }}",
      },
      {
        query: "irate(windows_net_packets_outbound_errors[1m])",
        legend: "sent from {{ nic }}",
      },
    ],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query:
          "(1-windows_logical_disk_free_bytes/windows_logical_disk_size_bytes)*100",
        legend: "{{ volume }}",
      },
    ],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      {
        query:
          "(1-windows_os_paging_free_bytes/windows_os_paging_limit_bytes)*100",
        legend: "usage",
      },
    ],
  },
];
