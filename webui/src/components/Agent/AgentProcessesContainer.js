import React, { useEffect } from 'react'
import { gql } from 'apollo-boost'

import AgentProcesses from './AgentProcesses'
import Loading from '../UI/Loading'
import Panel from '../UI/Panel'
import QueryError from '../UI/QueryError'
import { useFetch } from '../utils/hooks'
import { isNullOrUndefined } from '../utils'

const PROCESSES = gql`
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

const AgentProcessesContainer = () => {
  useEffect(() => {
    document.title = 'Processes | Glouton'
  }, [])

  const { isLoading, error, points, processes } = useFetch(PROCESSES, null, 10000)
  let displayProcesses
  if (isLoading) {
    displayProcesses = (
      <div className="marginOffset d-flex justify-content-center align-items-center">
        <Loading size="xl" />
      </div>
    )
  } else if (error || isNullOrUndefined(processes)) {
    displayProcesses = <QueryError noBorder />
  } else {
    const updatedAt = processes.updatedAt
    const systemMetrics = points
    const memRegexp = /^mem_/
    const loadRegexp = /^system_load/
    const swapRegexp = /^swap_/
    const cpuRegexp = /^cpu_/
    let memTypes = {}
    let loadTypes = {}
    let swapTypes = {}
    let cpuTypes = {}
    let uptime = 0
    let usersLogged = 0
    systemMetrics.forEach(m => {
      if (m.points) {
        if (memRegexp.test(m.labels.find(l => l.key === '__name__').value)) {
          memTypes[m.labels.find(l => l.key === '__name__').value] = m.points[m.points.length - 1].value
        }
        if (loadRegexp.test(m.labels.find(l => l.key === '__name__').value)) {
          loadTypes[m.labels.find(l => l.key === '__name__').value] = m.points[m.points.length - 1].value
        }
        if (swapRegexp.test(m.labels.find(l => l.key === '__name__').value)) {
          swapTypes[m.labels.find(l => l.key === '__name__').value] = m.points[m.points.length - 1].value
        }
        if (cpuRegexp.test(m.labels.find(l => l.key === '__name__').value)) {
          cpuTypes[m.labels.find(l => l.key === '__name__').value] = m.points[m.points.length - 1].value
        }
        if (m.labels.find(l => l.key === '__name__').value === 'uptime') {
          uptime = m.points[m.points.length - 1].value
        }
        if (m.labels.find(l => l.key === '__name__').value === 'users_logged') {
          usersLogged = m.points[m.points.length - 1].value
        }
      }
    })
    displayProcesses = (
      <AgentProcesses
        processes={processes.processes}
        updatedAt={updatedAt}
        sizePage={20}
        memTypes={memTypes}
        loadTypes={loadTypes}
        swapTypes={swapTypes}
        cpuTypes={cpuTypes}
        uptime={uptime}
        usersLogged={usersLogged}
      />
    )
  }

  return (
    <div style={{ marginTop: '1.5rem' }}>
      <Panel>{displayProcesses}</Panel>
    </div>
  )
}

export default AgentProcessesContainer
