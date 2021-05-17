import { chartTypes, UNIT_PERCENTAGE, UNIT_BYTE, UNIT_NUMBER } from "../utils";

export const gaugesBarPrometheusLinux = [
  {
    title: "CPU",
    metrics: [
      '(1-sum(irate(node_cpu_seconds_total{mode="idle"}[1m]))/sum(irate(node_cpu_seconds_total[1m])))*100',
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: [
      "(node_memory_MemTotal_bytes - node_memory_MemFree_bytes - node_memory_SReclaimable_bytes - node_memory_Cached_bytes - node_memory_Buffers_bytes)/node_memory_MemTotal_bytes*100",
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: ["irate(node_disk_io_time_seconds_total[1m])*100"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: [
      '(1-node_filesystem_avail_bytes{mountpoint="/"}/node_filesystem_size_bytes{mountpoint="/"})*100',
    ],
    unit: UNIT_PERCENTAGE,
  },
];

export const gaugesBarPrometheusWindows = [
  {
    title: "CPU",
    metrics: [
      '(1-sum(irate(node_cpu_seconds_total{mode="idle"}[1m]))/sum(irate(node_cpu_seconds_total[1m])))*100',
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: [
      "(node_memory_MemTotal_bytes - node_memory_MemFree_bytes - node_memory_SReclaimable_bytes - node_memory_Cached_bytes - node_memory_Buffers_bytes)/node_memory_MemTotal_bytes*100",
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: ["irate(node_disk_io_time_seconds_total[1m])*100"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: [
      '(1-node_filesystem_avail_bytes{mountpoint="/"}/node_filesystem_size_bytes{mountpoint="/"})*100',
    ],
    unit: UNIT_PERCENTAGE,
  },
];

export const gaugesBarBLEEMEO = [
  {
    title: "CPU",
    metrics: ["cpu_used"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: ["mem_used_perc"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: ["io_utilization"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: ["disk_used_perc"],
    mountpoint: "/",
    unit: UNIT_PERCENTAGE,
  },
];

export const widgetsBLEEMEO = [
  { title: "Processor Usage", type: chartTypes[1], unit: UNIT_PERCENTAGE },
  { title: "Memory Usage", type: chartTypes[1], unit: UNIT_BYTE },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["io_utilization"],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["io_read_bytes"],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["io_write_bytes"],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["io_reads"],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["io_writes"],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["net_packets_recv", "net_packets_sent"],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["net_err_in", "net_err_out"],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["disk_used_perc"],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["swap_used_perc"],
  },
];

export const widgetsPrometheusLinux = []; /*[
  {
    title: "Processor Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      "(sum by (mode)(irate(node_cpu_seconds_total[1m])) / ignoring(mode) group_left sum(irate(node_cpu_seconds_total[1m])))*100",
    ],
  },
  {
    title: "Memory Usage",
    type: chartTypes[1],
    unit: UNIT_BYTE,
    metrics: [
      "(node_memory_MemTotal_bytes - node_memory_MemFree_bytes - node_memory_SReclaimable_bytes - node_memory_Cached_bytes - node_memory_Buffers_bytes)",
    ],
  },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["irate(node_disk_io_time_seconds_total[1m])*100"],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["irate(node_disk_read_bytes_total[1m])"],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["irate(node_disk_write_bytes_total[1m])"],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["irate(node_disk_reads_completed_total[1m])"],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["irate(node_disk_reads_completed_total[1m])"],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      "irate(node_network_receive_bytes_total[1m])*8",
      "irate(node_network_transmit_bytes_total[1m])*8",
    ],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      "irate(node_network_receive_errs_total[1m])*8",
      "irate(node_network_transmit_errs_total[1m])*8",
    ],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      "(1-node_filesystem_avail_bytes{mountpoint=" /
        "}/node_filesystem_size_bytes{mountpoint=" /
        "})*100",
    ],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["(1-node_memory_SwapFree_bytes/node_memory_SwapTotal_bytes)*100"],
  },
];*/

export const widgetsPrometheusWindows = []; /*[
  {
    title: "Processor Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      "(sum by (mode)(irate(node_cpu_seconds_total[1m])) / ignoring(mode) group_left sum(irate(node_cpu_seconds_total[1m])))*100",
    ],
  },
  {
    title: "Memory Usage",
    type: chartTypes[1],
    unit: UNIT_BYTE,
    metrics: [
      "(node_memory_MemTotal_bytes - node_memory_MemFree_bytes - node_memory_SReclaimable_bytes - node_memory_Cached_bytes - node_memory_Buffers_bytes)",
    ],
  },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["irate(node_disk_io_time_seconds_total[1m])*100"],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["irate(node_disk_read_bytes_total[1m])"],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["irate(node_disk_write_bytes_total[1m])"],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["irate(node_disk_reads_completed_total[1m])"],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["irate(node_disk_reads_completed_total[1m])"],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      "irate(node_network_receive_bytes_total[1m])*8",
      "irate(node_network_transmit_bytes_total[1m])*8",
    ],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: [
      "irate(node_network_receive_errs_total[1m])*8",
      "irate(node_network_transmit_errs_total[1m])*8",
    ],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: [
      "(1-node_filesystem_avail_bytes{mountpoint=" /
        "}/node_filesystem_size_bytes{mountpoint=" /
        "})*100",
    ],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["(1-node_memory_SwapFree_bytes/node_memory_SwapTotal_bytes)*100"],
  },
];*/
