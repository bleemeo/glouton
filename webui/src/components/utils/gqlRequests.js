import { gql } from "@apollo/client";

// FACTS AND DETAILS

export const FACTS = gql`
  query facts {
    facts {
      name
      value
    }
  }
`;

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
`;

// CONTAINERS

export const CONTAINERS_DETAILS = gql`
  query containersDetails(
    $offset: Int!
    $limit: Int!
    $allContainers: Boolean!
    $search: String!
  ) {
    containers(
      input: { offset: $offset, limit: $limit }
      allContainers: $allContainers
      search: $search
    ) {
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
`;

export const CONTAINER_PROCESSES = gql`
  query containerProcesses($containerId: String!) {
    processes(containerId: $containerId) {
      Processes {
        pid
        cmdline
        name
        memory_rss
        cpu_percent
        cpu_time
        status
        username
      }
      Memory {
        Total
      }
    }
  }
`;

export const CONTAINER_SERVICE = gql`
  query containerService($containerId: String!) {
    containers(search: $containerId, allContainers: true) {
      containers {
        name
      }
    }
  }
`;

// PROCESSES

export const PROCESSES = gql`
  query processesQuery {
    processes {
      Time
      Uptime
      Loads
      Users
      Processes {
        pid
        ppid
        create_time
        cmdline
        name
        memory_rss
        cpu_percent
        cpu_time
        status
        username
        executable
        container_id
      }
      CPU {
        Nice
        System
        User
        Idle
        IOWait
        Guest
        GuestNice
        IRQ
        SoftIRQ
        Steal
      }
      Memory {
        Total
        Used
        Free
        Buffers
        Cached
      }
      Swap {
        Total
        Used
        Free
      }
    }
  }
`;
