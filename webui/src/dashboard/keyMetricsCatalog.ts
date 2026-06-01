// Per-service-type lookup of the 2-3 metrics worth showing in the
// service drawer. The PromQL metric names follow Glouton's emitted
// naming for each Telegraf input. If a metric is not present for a
// given install, the corresponding card silently doesn't render —
// adding an entry here is opt-in, not load-bearing.

export type ServiceKeyMetric = {
  // PromQL metric name (without the service filter, which is added at
  // query time).
  metric: string;
  // User-facing label.
  label: string;
  // How to format the value. "rate" applies a PromQL rate() so the
  // metric is read as per-second.
  format: "percent" | "bytes" | "count" | "rate";
  // Optional Y-axis cap for the sparkline (only useful for bounded
  // metrics like memory used %).
  yMax?: number;
  // Sparkline accent colour.
  color: string;
};

const ACCENT_BLUE = "#4F8DF5";
const ACCENT_VIOLET = "#8B5CF6";
const ACCENT_GREEN = "#10B981";

export const SERVICE_KEY_METRICS: Record<string, ServiceKeyMetric[]> = {
  redis: [
    { metric: "redis_memory", label: "Memory used", format: "bytes", color: ACCENT_BLUE },
    { metric: "redis_current_connections", label: "Connected clients", format: "count", color: ACCENT_VIOLET },
    { metric: "redis_total_operations", label: "Ops/s", format: "rate", color: ACCENT_GREEN },
  ],
  postgresql: [
    { metric: "postgresql_connections_used_perc", label: "Connections", format: "percent", yMax: 100, color: ACCENT_BLUE },
    { metric: "postgresql_commit", label: "Commits/s", format: "rate", color: ACCENT_VIOLET },
    { metric: "postgresql_rollback", label: "Rollbacks/s", format: "rate", color: "#EF4444" },
  ],
  mysql: [
    { metric: "mysql_threads", label: "Active threads", format: "count", color: ACCENT_BLUE },
    { metric: "mysql_queries", label: "Queries/s", format: "rate", color: ACCENT_VIOLET },
    { metric: "mysql_slow_queries", label: "Slow queries/s", format: "rate", color: "#F59E0B" },
  ],
  nginx: [
    { metric: "nginx_requests", label: "Requests/s", format: "rate", color: ACCENT_BLUE },
    { metric: "nginx_connections_active", label: "Active connections", format: "count", color: ACCENT_VIOLET },
  ],
  apache: [
    { metric: "apache_requests", label: "Requests/s", format: "rate", color: ACCENT_BLUE },
    { metric: "apache_busy_workers", label: "Busy workers", format: "count", color: ACCENT_VIOLET },
  ],
  mongodb: [
    { metric: "mongodb_connections_current", label: "Connections", format: "count", color: ACCENT_BLUE },
    { metric: "mongodb_queries", label: "Queries/s", format: "rate", color: ACCENT_VIOLET },
  ],
  rabbitmq: [
    { metric: "rabbitmq_queue_messages", label: "Queued messages", format: "count", color: ACCENT_BLUE },
    { metric: "rabbitmq_queue_consumers", label: "Consumers", format: "count", color: ACCENT_VIOLET },
    { metric: "rabbitmq_queue_messages_unacked", label: "Unacked", format: "count", color: "#F59E0B" },
  ],
  elasticsearch: [
    { metric: "elasticsearch_search_query", label: "Queries/s", format: "rate", color: ACCENT_BLUE },
    { metric: "elasticsearch_docs_count", label: "Documents", format: "count", color: ACCENT_VIOLET },
  ],
  cassandra: [
    { metric: "cassandra_jvm_heap_used", label: "JVM heap", format: "bytes", color: ACCENT_BLUE },
    { metric: "cassandra_read_requests", label: "Reads/s", format: "rate", color: ACCENT_VIOLET },
  ],
  haproxy: [
    { metric: "haproxy_scur", label: "Current sessions", format: "count", color: ACCENT_BLUE },
    { metric: "haproxy_req_tot", label: "Requests/s", format: "rate", color: ACCENT_VIOLET },
  ],
  memcached: [
    { metric: "memcached_curr_connections", label: "Connections", format: "count", color: ACCENT_BLUE },
    { metric: "memcached_get_hits", label: "Cache hits/s", format: "rate", color: ACCENT_VIOLET },
  ],
  nats: [
    { metric: "nats_connections", label: "Connections", format: "count", color: ACCENT_BLUE },
    { metric: "nats_in_msgs", label: "Incoming msg/s", format: "rate", color: ACCENT_VIOLET },
  ],
  zookeeper: [
    { metric: "zookeeper_num_alive_connections", label: "Connections", format: "count", color: ACCENT_BLUE },
    { metric: "zookeeper_outstanding_requests", label: "Outstanding requests", format: "count", color: ACCENT_VIOLET },
  ],
};

export function keyMetricsFor(serviceName: string): ServiceKeyMetric[] {
  return SERVICE_KEY_METRICS[serviceName.toLowerCase()] ?? [];
}
