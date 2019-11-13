import { gql } from 'apollo-boost'

// FACTS AND DETAILS

export const FACTS = gql`
  query facts {
    facts {
      name
      value
    }
  }
`

export const AGENT_DETAILS = gql`
  query agent_details {
    services(isActive: true) {
      name
      containerId
      ipAddress
      listenAddresses
      exePath
      active
      status
      statusDescription
    }
    agentInformation {
      registrationAt
      lastReport
      isConnected
    }
    tags {
      tagName
    }
    agentStatus {
      status
      statusDescription
    }
  }
`

// CONTAINERS

export const CONTAINERS_DETAILS = gql`
  query containersDetails($offset: Int!, $limit: Int!, $allContainers: Boolean!, $search: String!) {
    containers(input: { offset: $offset, limit: $limit }, allContainers: $allContainers, search: $search) {
      count
      currentCount
      containers {
        command
        createdAt
        id
        image
        inspectJSON
        name
        startedAt
        state
        finishedAt
        ioWriteBytes
        ioReadBytes
        netBitsRecv
        netBitsSent
        memUsedPerc
        cpuUsedPerc
      }
    }
  }
`

export const CONTAINER_PROCESSES = gql`
  query containerProcesses($containerId: String!) {
    processes(containerId: $containerId) {
      processes {
        pid
        cmdline
        name
        memory_rss
        cpu_percent
        cpu_time
        status
        username
      }
    }
    points(
      metricsFilter: [
        { labels: { key: "__name__", value: "mem_buffered" } }
        { labels: { key: "__name__", value: "mem_cached" } }
        { labels: { key: "__name__", value: "mem_free" } }
        { labels: { key: "__name__", value: "mem_used" } }
      ]
      start: ""
      end: ""
      minutes: 15
    ) {
      labels {
        key
        value
      }
      points {
        time
        value
      }
    }
  }
`

export const CONTAINER_SERVICE = gql`
  query containerService($containerId: String!) {
    containers(search: $containerId, allContainers: true) {
      containers {
        name
      }
    }
  }
`

// PROCESSES

export const PROCESSES = gql`
  query processesQuery {
    processes {
      updatedAt
      processes {
        pid
        ppid
        cmdline
        name
        memory_rss
        cpu_percent
        cpu_time
        status
        username
      }
    }
    points(
      metricsFilter: [
        { labels: { key: "__name__", value: "mem_buffered" } }
        { labels: { key: "__name__", value: "mem_cached" } }
        { labels: { key: "__name__", value: "mem_free" } }
        { labels: { key: "__name__", value: "mem_used" } }
        { labels: { key: "__name__", value: "system_load1" } }
        { labels: { key: "__name__", value: "system_load5" } }
        { labels: { key: "__name__", value: "system_load15" } }
        { labels: { key: "__name__", value: "swap_free" } }
        { labels: { key: "__name__", value: "swap_used" } }
        { labels: { key: "__name__", value: "swap_total" } }
        { labels: { key: "__name__", value: "cpu_system" } }
        { labels: { key: "__name__", value: "cpu_user" } }
        { labels: { key: "__name__", value: "cpu_nice" } }
        { labels: { key: "__name__", value: "cpu_wait" } }
        { labels: { key: "__name__", value: "cpu_idle" } }
        { labels: { key: "__name__", value: "uptime" } }
        { labels: { key: "__name__", value: "users_logged" } }
      ]
      start: ""
      end: ""
      minutes: 15
    ) {
      labels {
        key
        value
      }
      points {
        time
        value
      }
    }
  }
`

// GRAPHS

export const GET_POINTS = gql`
  query Points($metricsFilter: [MetricInput!]!, $start: String!, $end: String!, $minutes: Int!) {
    points(metricsFilter: $metricsFilter, start: $start, end: $end, minutes: $minutes) {
      labels {
        key
        value
      }
      points {
        time
        value
      }
      thresholds {
        highWarning
        highCritical
      }
    }
  }
`
