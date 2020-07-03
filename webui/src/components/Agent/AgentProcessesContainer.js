import React, { useEffect, useMemo } from 'react'

import AgentProcesses from './AgentProcesses'
import Loading from '../UI/Loading'
import Panel from '../UI/Panel'
import QueryError from '../UI/QueryError'
import { useFetch } from '../utils/hooks'
import { isNullOrUndefined, LabelName } from '../utils'
import FetchSuspense from '../UI/FetchSuspense'
import { PROCESSES } from '../utils/gqlRequests'

const AgentProcessesContainer = () => {
  useEffect(() => {
    document.title = 'Processes | Glouton'
  }, [])

  const { isLoading, error, points, processes } = useFetch(PROCESSES, null, 10000)
  return (
    <FetchSuspense
      isLoading={isLoading}
      error={error || isNullOrUndefined(processes)}
      loadingComponent={
        <div className="marginOffset d-flex justify-content-center align-items-center">
          <Loading size="xl" />
        </div>
      }
      fallbackComponent={<QueryError noBorder />}
      processes={processes}
      points={points}
    >
      {({ processes, points }) => {
        const updatedAt = processes.updatedAt
        const { memTypes, loadTypes, swapTypes, cpuTypes, uptime, usersLogged } = useMemo(() => getSystemInfo(points), [
          points
        ])
        return (
          <div style={{ marginTop: '1.5rem' }}>
            <Panel>
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
            </Panel>
          </div>
        )
      }}
    </FetchSuspense>
  )
}

const getSystemInfo = points => {
  const systemMetrics = points
  const memRegexp = /^mem_/
  const loadRegexp = /^system_load/
  const swapRegexp = /^swap_/
  const cpuRegexp = /^cpu_/
  const memTypes = {}
  const loadTypes = {}
  const swapTypes = {}
  const cpuTypes = {}
  let uptime = 0
  let usersLogged = 0
  systemMetrics.forEach(m => {
    if (m.points) {
      if (memRegexp.test(m.labels.find(l => l.key === LabelName).value)) {
        memTypes[m.labels.find(l => l.key === LabelName).value] = m.points[m.points.length - 1].value
      }
      if (loadRegexp.test(m.labels.find(l => l.key === LabelName).value)) {
        loadTypes[m.labels.find(l => l.key === LabelName).value] = m.points[m.points.length - 1].value
      }
      if (swapRegexp.test(m.labels.find(l => l.key === LabelName).value)) {
        swapTypes[m.labels.find(l => l.key === LabelName).value] = m.points[m.points.length - 1].value
      }
      if (cpuRegexp.test(m.labels.find(l => l.key === LabelName).value)) {
        cpuTypes[m.labels.find(l => l.key === LabelName).value] = m.points[m.points.length - 1].value
      }
      if (m.labels.find(l => l.key === LabelName).value === 'uptime') {
        uptime = m.points[m.points.length - 1].value
      }
      if (m.labels.find(l => l.key === LabelName).value === 'users_logged') {
        usersLogged = m.points[m.points.length - 1].value
      }
    }
  })
  return { memTypes, loadTypes, swapTypes, cpuTypes, uptime, usersLogged }
}

export default AgentProcessesContainer
