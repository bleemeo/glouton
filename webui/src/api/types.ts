// Wire types matching Glouton's local /data/* and /api/v1/* responses.

export type StoreInfo = {
  persistent: boolean;
  retention_seconds: number;
  oldest_point_ms: number;
};

export type AgentInformation = {
  registrationAt?: string;
  lastReport?: string;
  isConnected: boolean;
};

export type Fact = {
  name: string;
  value: string;
};

export type Service = {
  name: string;
  containerId: string;
  ipAddress: string;
  listenAddresses: string[];
  exePath: string;
  active: boolean;
  status: number;
  statusDescription?: string;
};

export type Container = {
  id: string;
  name: string;
  image: string;
  state: string;
  command: string;
  inspectJSON: string;
  createdAt?: string;
  startedAt?: string;
  finishedAt?: string;
  ioWriteBytes: number;
  ioReadBytes: number;
  netBitsRecv: number;
  netBitsSent: number;
  memUsedPerc: number;
  cpuUsedPerc: number;
};

export type ContainersResponse = {
  count: number;
  currentCount: number;
  containers: Container[];
};

export type Process = {
  pid: number;
  ppid: number;
  create_time: string;
  cmdline: string;
  name: string;
  memory_rss: number;
  cpu_percent: number;
  cpu_time: number;
  status: string;
  username: string;
  executable: string;
  container_id: string;
};

export type Topinfo = {
  Time: string;
  Uptime: number;
  Loads: number[];
  Users: number;
  Processes: Process[];
  CPU?: { User: number; System: number; Idle: number; IOWait: number; Steal: number };
  Memory?: { Total: number; Used: number; Free: number; Buffers: number; Cached: number };
  Swap?: { Total: number; Used: number; Free: number };
};

export type Monitor = {
  name: string;
  url: string;
  module: string;
  scheme: string;
  source: "config" | "bleemeo";
};

export type MonitorsResponse = {
  monitors: Monitor[];
};

// PromQL matrix response (subset used by the dashboard).
export type PromQLValue = [number, string]; // [unix-ts seconds, stringified float]

export type PromQLSeries = {
  metric: Record<string, string>;
  values: PromQLValue[];
};

export type PromQLResponse = {
  status: "success" | "error";
  data: { resultType: "matrix"; result: PromQLSeries[] };
  error?: string;
};
