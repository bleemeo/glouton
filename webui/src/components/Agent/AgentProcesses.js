import d3 from 'd3'
import React from 'react'
import PropTypes from 'prop-types'
import { Form } from 'tabler-react'

import { createFilterFn, isNullOrUndefined, isEmpty } from '../utils'
import { bytesToString, _formatCpuTime, formatToBytes, formatDateTimeWithSeconds } from '../utils/formater'
import ProcessesTable, { formatCmdLine, GraphCell } from '../UI/ProcessesTable'
import FaIcon from '../UI/FaIcon'

const colorScale = d3.scale.category20()

// taken from the Agent code
const formatUptime = uptimeSeconds => {
  const uptimeDays = Math.trunc(uptimeSeconds / (24 * 60 * 60))
  const uptimeHours = Math.trunc((uptimeSeconds % (24 * 60 * 60)) / (60 * 60))
  const uptimeMinutes = Math.trunc((uptimeSeconds % (60 * 60)) / 60)

  const textMinutes = uptimeDays > 1 ? 'minutes' : 'minute'
  const textHours = uptimeHours > 1 ? 'hours' : 'hour'
  const textDays = uptimeMinutes > 1 ? 'days' : 'day'

  let uptimeString
  if (uptimeDays === 0 && uptimeHours === 0) {
    uptimeString = `${uptimeMinutes} ${textMinutes}`
  } else if (uptimeDays === 0) {
    uptimeString = `${uptimeHours} ${textHours}`
  } else {
    uptimeString = `${uptimeDays} ${textDays}, ${uptimeHours} ${textHours}`
  }

  return uptimeString
}

export default class AgentProcesses extends React.Component {
  static propTypes = {
    processes: PropTypes.instanceOf(Array).isRequired,
    updatedAt: PropTypes.string.isRequired,
    sizePage: PropTypes.number.isRequired,
    memTypes: PropTypes.object.isRequired,
    loadTypes: PropTypes.object.isRequired,
    swapTypes: PropTypes.object.isRequired,
    cpuTypes: PropTypes.object.isRequired,
    uptime: PropTypes.number.isRequired,
    usersLogged: PropTypes.number.isRequired
  }

  state = {
    filter: '',
    field: 'cpu_percent',
    order: 'asc',
    usernamesFilter: []
  }

  showRowDetail = row => {
    const { processes } = this.props
    const { order, field } = this.state
    const filteredProcesses = this.getFilteredProcesses()
    const processesWithSamePPID = filteredProcesses.filter(p => row.ppid === p.ppid)
    const processParent = processes.find(p => row.ppid === p.pid)
    processesWithSamePPID.sort((a, b) => {
      if (typeof a[field] === 'string' && typeof b[field] === 'string') {
        return order === 'asc' ? a[field].localeCompare(b[field]) : b[field].localeCompare(a[field])
      } else {
        return order === 'asc' ? a[field] - b[field] : b[field] - a[field]
      }
    })
    if (processesWithSamePPID.length === 1) return null
    return (
      <div style={{ backgroundColor: '#f2f2f2' }}>
        {processParent ? (
          <table style={{ width: '100%', marginLeft: '-0.2rem' }} className="borderless">
            <tbody>
              <tr>
                <td style={{ width: '6.4rem' }}>{processParent.pid}</td>
                <td style={{ width: '7rem' }}>{processParent.username}</td>
                <td style={{ width: '5rem' }}>
                  {!isNullOrUndefined(processParent.memory_rss)
                    ? formatToBytes(processParent.memory_rss * 1024).join(' ')
                    : ''}
                </td>
                <td style={{ width: '5rem' }}>{processParent.status}</td>
                <td style={{ width: '7rem' }}>
                  {!isNullOrUndefined(processParent.cpu_percent) && !isNaN(processParent.cpu_percent) ? (
                    <GraphCell value={processParent.cpu_percent} />
                  ) : null}
                </td>
                <td style={{ width: '7rem' }}>
                  {!isNullOrUndefined(processParent.mem_percent) && !isNaN(processParent.mem_percent) ? (
                    <GraphCell value={processParent.mem_percent} />
                  ) : null}
                </td>
                <td style={{ width: '4.5rem' }}>{processParent.new_cpu_times}</td>
                <td className="cellEllipsis">{formatCmdLine(processParent.cmdline, null)}</td>
              </tr>
            </tbody>
          </table>
        ) : null}
        <table style={{ width: '100%', marginLeft: '-0.2rem' }} className="borderless">
          <tbody>
            {processesWithSamePPID.map((process, idx) => (
              <tr key={process.pid}>
                <td style={{ width: '2rem' }}>
                  <FaIcon icon="fa fa-level-up-alt fa-rotate-90" />
                </td>
                <td style={{ width: '5rem' }}>{process.pid}</td>
                <td style={{ width: '7rem' }}>{process.username}</td>
                <td style={{ width: '5rem' }}>
                  {!isNullOrUndefined(process.memory_rss) ? formatToBytes(process.memory_rss * 1024).join(' ') : ''}
                </td>
                <td style={{ width: '5rem' }}>{process.status}</td>
                <td style={{ width: '7rem' }}>
                  {!isNullOrUndefined(process.cpu_percent) && !isNaN(process.cpu_percent) ? (
                    <GraphCell value={process.cpu_percent} />
                  ) : null}
                </td>
                <td style={{ width: '7rem' }}>
                  {!isNullOrUndefined(process.mem_percent) && !isNaN(process.mem_percent) ? (
                    <GraphCell value={process.mem_percent} />
                  ) : null}
                </td>
                <td style={{ width: '5rem' }}>{process.new_cpu_times}</td>
                <td className="cellEllipsis" style={{ color: '#000', width: '42vw' }}>
                  {formatCmdLine(process.cmdline, null)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  showExpandIndicator = ({ expanded, expandable }) => {
    if (!expandable) return null
    else if (expanded) {
      return (
        <b>
          <FaIcon icon="fa fa-caret-down" />
        </b>
      )
    }
    return (
      <b>
        <FaIcon icon="fa fa-caret-right" />
      </b>
    )
  }

  getFilteredProcesses = () => {
    const { processes } = this.props
    const { filter, usernamesFilter } = this.state
    const filterFn = createFilterFn(filter)
    return processes.filter(proc => {
      return (
        (usernamesFilter.length === 0 ? true : usernamesFilter.includes(proc.username)) &&
        (filterFn(proc.pid.toString()) ||
          filterFn(proc.ppid ? proc.ppid.toString() : '') ||
          filterFn(proc.username) ||
          filterFn(proc.cmdline) ||
          filterFn(proc.name))
      )
    })
  }

  handleUsersFilter = username => {
    const prevUsernamesFilter = Array.from(this.state.usernamesFilter)
    if (prevUsernamesFilter.includes(username)) prevUsernamesFilter.splice(prevUsernamesFilter.indexOf(username), 1)
    else prevUsernamesFilter.push(username)
    this.setState({
      usernamesFilter: prevUsernamesFilter
    })
  }

  render() {
    const { processes, sizePage, memTypes, loadTypes, swapTypes, cpuTypes, updatedAt, uptime, usersLogged } = this.props
    const { filter, usernamesFilter } = this.state

    let info = null
    let filterInput = null
    let processesTable = null
    if (
      !isEmpty(cpuTypes) &&
      !isEmpty(memTypes) &&
      !isEmpty(swapTypes) &&
      !isEmpty(loadTypes) &&
      processes &&
      updatedAt &&
      uptime
    ) {
      // sometimes the sum of all CPU percentage make more than 100%
      // which broke the bar, so we have to re-compute them
      const cpuTotal = Object.values(cpuTypes).reduce((acc, v) => acc + v)
      const cpuSystemPerc = (cpuTypes['cpu_system'] / cpuTotal) * 100
      const cpuUserPerc = (cpuTypes['cpu_user'] / cpuTotal) * 100
      const cpuNicePerc = (cpuTypes['cpu_nice'] / cpuTotal) * 100
      const cpuWaitPerc = (cpuTypes['cpu_wait'] / cpuTotal) * 100
      const cpuIdlePerc = (cpuTypes['cpu_idle'] / cpuTotal) * 100

      const cpuTooltipMsg =
        `${d3.format('.2r')(cpuTypes['cpu_system'])}% system` +
        ` ‒ ${d3.format('.2r')(cpuTypes['cpu_user'])}% user` +
        ` ‒ ${d3.format('.2r')(cpuTypes['cpu_nice'])}% nice` +
        ` ‒ ${d3.format('.2r')(cpuTypes['cpu_wait'])}% wait` +
        ` ‒ ${d3.format('.2r')(cpuTypes['cpu_idle'])}% idle`

      let memTotal = Object.values(memTypes).reduce((acc, v) => acc + v)

      let memUsed = memTypes['mem_used']
      const memUsedPerc = (memUsed / memTotal) * 100
      const memFreePerc = (memTypes['mem_free'] / memTotal) * 100
      const memBuffersPerc = (memTypes['mem_buffered'] / memTotal) * 100
      const memCachedPerc = (memTypes['mem_cached'] / memTotal) * 100

      const memTooltipMsg =
        `${bytesToString(memUsed)} used` +
        ` ‒ ${bytesToString(memTypes['mem_buffered'])} buffers` +
        ` ‒ ${bytesToString(memTypes['mem_cached'])} cached` +
        ` ‒ ${bytesToString(memTypes['mem_free'])} free`

      const swapUsedPerc = (swapTypes['swap_used'] / swapTypes['swap_total']) * 100
      const swapFreePerc = (swapTypes['swap_free'] / swapTypes['swap_total']) * 100

      const swapTooltipMsg =
        `${bytesToString(swapTypes['swap_used'])} used` + ` ‒ ${bytesToString(swapTypes['swap_free'])} free`
      filterInput = (
        <Form.Input
          className="mb-3 form-control form-control-sm w-25"
          icon="search"
          value={filter}
          onChange={e => this.setState({ filter: e.target.value })}
        />
      )
      const timeDate = new Date(updatedAt)
      const maxLoad = Math.max(...Object.values(loadTypes))
      const loadTooltipMdg =
        loadTypes['system_load1'] + '\n' + loadTypes['system_load5'] + '\n' + loadTypes['system_load15']

      const usernames = []
      const filterFn = createFilterFn(this.state.filter)
      const searchedProcesses = processes.filter(proc => {
        return (
          filterFn(proc.pid.toString()) ||
          filterFn(proc.ppid ? proc.ppid.toString() : '') ||
          filterFn(proc.username) ||
          filterFn(proc.cmdline) ||
          filterFn(proc.name)
        )
      })
      searchedProcesses.map(process => {
        if (!usernames.includes(process.username)) usernames.push(process.username)
      })

      info = (
        <div className="row">
          <div className="col-xl-8 col-sm-12">
            <table className="table table-sm" style={{ marginBottom: 0 }}>
              <tbody>
                <tr>
                  <td className="percent-bar-label">
                    <strong>Cpu(s):</strong>
                  </td>
                  <td>
                    <div className="percent-bars">
                      <PercentBar color="#e67e22" percent={cpuSystemPerc} title={cpuTooltipMsg} />
                      <PercentBar color="#3498db" percent={cpuUserPerc} title={cpuTooltipMsg} />
                      <PercentBar color="#4DD0E1" percent={cpuNicePerc} title={cpuTooltipMsg} />
                      <PercentBar color="#e74c3c" percent={cpuWaitPerc} title={cpuTooltipMsg} />
                      <PercentBar color="#2ecc71" percent={cpuIdlePerc} title={cpuTooltipMsg} />
                    </div>
                  </td>
                </tr>
                <tr>
                  <td className="percent-bar-label">
                    <strong>Mem:</strong>
                  </td>
                  <td>
                    <div className="percent-bars">
                      <PercentBar color="#3498db" percent={memUsedPerc} title={memTooltipMsg} />
                      <PercentBar color="#95a5a6" percent={memBuffersPerc} title={memTooltipMsg} />
                      <PercentBar color="#f1c40f" percent={memCachedPerc} title={memTooltipMsg} />
                      <PercentBar color="#2ecc71" percent={memFreePerc} title={memTooltipMsg} />
                    </div>
                  </td>
                </tr>
                <tr>
                  <td className="percent-bar-label">
                    <strong>Swap:</strong>
                  </td>
                  <td>
                    <div className="percent-bars">
                      <PercentBar color="#3498db" percent={swapUsedPerc} title={swapTooltipMsg} />
                      <PercentBar color="#2ecc71" percent={swapFreePerc} title={swapTooltipMsg} />
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div className="col-xl-1 col-sm-4">
            <h4 style={{ textAlign: 'center' }}>Quick filter</h4>
            <div>
              <h4>
                {' '}
                <span className="badge badge-info">{usernames.length <= 1 ? 'User:' : 'Users:'}</span>
              </h4>
              <div style={{ height: '7rem', overflowY: 'scroll', overflowX: 'hidden' }}>
                {usernames.sort().map(username => (
                  <div className="form-check" key={username}>
                    <input
                      className="form-check-input"
                      type="checkbox"
                      checked={usernamesFilter.includes(username)}
                      onChange={() => this.handleUsersFilter(username)}
                    />
                    <label className="form-check-label">{username}</label>
                  </div>
                ))}
              </div>
            </div>
          </div>
          <div className="col-xl-3 col-sm-8">
            <table className="table table-sm" style={{ marginBottom: 0 }}>
              <tbody>
                <tr>
                  <td colSpan="2">
                    <h4 style={{ marginBottom: 0 }}>
                      <strong style={{ fontSize: 'medium' }}>Last update: </strong>
                      <span className="badge badge-secondary">{formatDateTimeWithSeconds(timeDate)}</span>
                    </h4>
                  </td>
                </tr>
                <tr>
                  <td colSpan="5">
                    <strong>Users:</strong> {usersLogged}
                  </td>
                </tr>
                <tr>
                  <td colSpan="5">
                    <strong>Uptime:</strong> {formatUptime(uptime)}
                  </td>
                </tr>
                <tr>
                  <td colSpan="5">
                    <div style={{ display: 'flex', alignItems: 'left', justifyContent: 'right', flexDirection: 'row' }}>
                      <div style={{ width: '30%' }}>
                        <strong>Load average:</strong>
                      </div>
                      <div
                        style={{
                          display: 'flex',
                          alignItems: 'left',
                          justifyContent: 'right',
                          flexDirection: 'column',
                          width: '70%'
                        }}
                      >
                        <div className="percent-bars littleBorderRadius" style={{ height: '8px' }}>
                          <PercentBar
                            color={colorScale(0)}
                            percent={(loadTypes['system_load1'] / maxLoad) * 100}
                            title={loadTooltipMdg}
                          />
                        </div>
                        <div className="percent-bars littleBorderRadius" style={{ height: '8px' }}>
                          <PercentBar
                            color={colorScale(1)}
                            percent={(loadTypes['system_load5'] / maxLoad) * 100}
                            title={loadTooltipMdg}
                          />
                        </div>
                        <div className="percent-bars littleBorderRadius" style={{ height: '8px' }}>
                          <PercentBar
                            color={colorScale(2)}
                            percent={(loadTypes['system_load15'] / maxLoad) * 100}
                            title={loadTooltipMdg}
                          />
                        </div>
                      </div>
                    </div>
                  </td>
                </tr>
                <tr>
                  <td colSpan="5">
                    <strong>Tasks:</strong>
                  </td>
                </tr>
              </tbody>
            </table>
            <table className="table table-sm" style={{ marginBottom: 0 }}>
              <tbody>
                <tr>
                  <td className="smaller">{processes.length} total</td>
                  <td className="smaller">{processes.filter(p => p.status === 'running').length} running</td>
                  <td className="smaller">
                    {
                      processes.filter(
                        p =>
                          p.status === 'sleeping' ||
                          p.status === '?' ||
                          p.status === 'idle' ||
                          p.status === 'disk-sleep'
                      ).length
                    }{' '}
                    sleeping
                  </td>
                  <td className="smaller">{processes.filter(p => p.status === 'stopped').length} stopped</td>
                  <td className="smaller">{processes.filter(p => p.status === 'zombie').length} zombie</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      )
    }
    if (processes && !isEmpty(memTypes)) {
      let memTotal = Object.values(memTypes).reduce((acc, v) => acc + v)
      const filteredProcesses = this.getFilteredProcesses()
      const processesTmp = filteredProcesses.map(process => {
        process.mem_percent = parseFloat(d3.format('.2r')(((process.memory_rss * 1024) / memTotal) * 100))
        process.new_cpu_times = _formatCpuTime(process.cpu_time)
        return process
      })

      const childrenProcesses = new Map()
      processesTmp.map(process => {
        if (process.ppid !== undefined) {
          const nodeProcessChildrens = childrenProcesses.get(process.ppid) || []
          nodeProcessChildrens.push(process)
          childrenProcesses.set(process.ppid, nodeProcessChildrens)
        }
      })
      const processesLeaves = []
      const processesNodes = []

      const finalProcesses = []

      processesTmp.map(process => {
        const siblingsProcesses = childrenProcesses.get(process.ppid)
        if (process.ppid === undefined || process.ppid === 1 || !processes.find(p => process.ppid === p.pid)) {
          processesNodes.push(process)
        } else if (
          siblingsProcesses.length > 1 &&
          !siblingsProcesses.some(
            pBrother => childrenProcesses.get(pBrother.pid) && childrenProcesses.get(pBrother.pid).length
          )
        ) {
          processesLeaves.push(process)
        } else {
          processesNodes.push(process)
        }
      })
      const previousProcesses = []
      processesLeaves.map(process => {
        const processesWithSameParents = childrenProcesses.get(process.ppid)
        const processParent = processes.find(p => process.ppid === p.pid)
        if (!previousProcesses.includes(processParent.pid)) {
          previousProcesses.push(processParent.pid)
          const totalRes = [...processesWithSameParents, processParent]
            .map(p => (!isNullOrUndefined(p.memory_rss) ? p.memory_rss : 0))
            .reduce((acc, val) => acc + val)
          const totalCpu = processesWithSameParents
            .concat(processParent ? [processParent] : [])
            .map(p => (!isNullOrUndefined(p.cpu_percent) && !isNaN(p.cpu_percent) ? p.cpu_percent : 0))
            .reduce((acc, val) => acc + val)
          const totalMem = [...processesWithSameParents, processParent]
            .map(p => (!isNullOrUndefined(p.mem_percent) && !isNaN(p.mem_percent) ? p.mem_percent : 0))
            .reduce((acc, val) => parseFloat(acc) + parseFloat(val))
          finalProcesses.push({
            ...process,
            username: [...processesWithSameParents, processParent].every(p => p.username === process.username)
              ? process.username
              : '...',
            memory_rss: totalRes,
            cpu_percent: totalCpu,
            mem_percent: totalMem,
            status: [...processesWithSameParents, processParent].every(p => p.status === process.status)
              ? process.status
              : '...',
            new_cpu_times: _formatCpuTime(
              [...processesWithSameParents, processParent]
                .map(p => (!isNullOrUndefined(p.cpu_time) ? p.cpu_time : 0))
                .reduce((acc, v) => acc + v)
            ),
            cpu_time: processesWithSameParents
              .map(p => (!isNullOrUndefined(p.cpu_time) ? p.cpu_time : 0))
              .reduce((acc, val) => acc + val),
            cmdline: processParent.name,
            pid: processParent.pid,
            expandable: true
          })
        }
      })
      processesNodes
        .filter(process => !previousProcesses.includes(process.pid))
        .map(process => {
          finalProcesses.push({ ...process, cmdline: process.cmdline, expandable: false })
        })

      const expandRow = {
        renderer: this.showRowDetail,
        showExpandColumn: true,
        expandHeaderColumnRenderer: ({ isAnyExpands }) => {
          if (isAnyExpands) {
            return <FaIcon icon="fa fa-caret-down" />
          }
          return (
            <b>
              <FaIcon icon="fa fa-caret-right" />
            </b>
          )
        },
        expandColumnRenderer: this.showExpandIndicator,
        nonExpandable: processesNodes.filter(process => !previousProcesses.includes(process.pid)).map(p => p.pid)
      }
      processesTable = (
        <ProcessesTable
          data={finalProcesses}
          sizePage={sizePage}
          expandRow={expandRow}
          borderless
          onSortTable={sort => this.setState(sort)}
          renderLoadMoreButton
          classNames="fontSmaller"
        />
      )
    }
    return (
      <div className="marginOffset">
        {info}
        {filterInput}
        {processesTable}
      </div>
    )
  }
}

const PercentBar = ({ color, title, percent }) => (
  <div
    className="percent-bar"
    title={title}
    data-toggle={'tooltip'}
    style={{ backgroundColor: color, width: percent + '%' }}
  />
)

PercentBar.propTypes = {
  color: PropTypes.string.isRequired,
  title: PropTypes.string.isRequired,
  percent: PropTypes.number.isRequired
}
