export interface AgentInfo {
  registrationAt?: string;
  lastReport?: string;
  isConnected: boolean;
}

export interface AgentStatus {
  serviceName: string;
  status: number;
  statusDescription: string;
}

export interface CPUUsage {
  User: number;
  Nice: number;
  System: number;
  Idle: number;
  IOWait: number;
  Guest: number;
  GuestNice: number;
  Irq: number;
  SoftIrq: number;
  Steal: number;
}

export interface Container {
  command: string;
  createdAt?: string;
  id: string;
  image: string;
  inspectJSON: string;
  name: string;
  startedAt?: string;
  state: string;
  finishedAt?: string;
  ioWriteBytes: number;
  ioReadBytes: number;
  netBitsRecv: number;
  netBitsSent: number;
  memUsedPerc: number;
  cpuUsedPerc: number;
}

export interface Containers {
  count: number;
  currentCount: number;
  containers: Container[];
}

export interface Fact {
  name: string;
  value: string;
}

export interface MemoryUsage {
  Total: number;
  Used: number;
  Free: number;
  Buffers: number;
  Cached: number;
}

export interface Process {
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
  mem_percent?: number;
  new_cpu_times?: string;
  expandable?: boolean;
}

export interface Service {
  name: string;
  containerId: string;
  ipAddress: string;
  listenAddresses: string[];
  exePath: string;
  active: boolean;
  status: number;
  statusDescription?: string;
}

export interface SwapUsage {
  Total: number;
  Used: number;
  Free: number;
}

export interface Tag {
  tagName: string;
}

export interface Topinfo {
  Time: string;
  Uptime: number;
  Loads: number[];
  Users: number;
  Processes: Process[];
  CPU?: CPUUsage;
  Memory?: MemoryUsage;
  Swap?: SwapUsage;
}

export interface Log {
  timestamp: string;
  message: string;
}
