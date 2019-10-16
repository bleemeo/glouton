import React, { useEffect, useState } from 'react'
import VisibilitySensor from 'react-visibility-sensor'
import WidgetDashboardItem from '../UI/WidgetDashboardItem'
import { chartTypes, UNIT_PERCENTAGE, UNIT_BYTE, UNIT_NUMBER, computeBackwardForward } from '../utils'
import { formatDateTime } from '../utils/formater'
import EditPeriodModal, { lastQuickRanges } from './EditPeriodModal'
import MetricGaugeItem from '../Metric/MetricGaugeItem'
import LineChart from '../UI/LineChart'

const gaugesBar = [
  { title: 'CPU', name: 'cpu_used', unit: UNIT_PERCENTAGE },
  { title: 'Memory', name: 'mem_used_perc', unit: UNIT_PERCENTAGE },
  { title: 'IO', name: 'io_utilization', unit: UNIT_PERCENTAGE },
  { title: '/', name: 'disk_used_perc', item: '/', unit: UNIT_PERCENTAGE }
]

const widgets = [
  { title: 'Processor Usage', type: chartTypes[1], unit: UNIT_PERCENTAGE },
  { title: 'Memory Usage', type: chartTypes[1], unit: UNIT_BYTE },
  {
    title: 'Disk IO Utilization',
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    namesItems: [{ name: 'io_utilization' }]
  },
  { title: 'Disk Read Bytes', type: chartTypes[2], unit: UNIT_BYTE, namesItems: [{ name: 'io_read_bytes' }] },
  { title: 'Disk Write Bytes', type: chartTypes[2], unit: UNIT_BYTE, namesItems: [{ name: 'io_write_bytes' }] },
  { title: 'Disk Read Number', type: chartTypes[2], unit: UNIT_NUMBER, namesItems: [{ name: 'io_reads' }] },
  { title: 'Disk Write Number', type: chartTypes[2], unit: UNIT_NUMBER, namesItems: [{ name: 'io_writes' }] },
  {
    title: 'Network Packets',
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    namesItems: [{ name: 'net_packets_recv' }, { name: 'net_packets_sent' }]
  },
  {
    title: 'Network Errors',
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    namesItems: [{ name: 'net_err_in' }, { name: 'net_err_out' }]
  },
  { title: 'Disk Space', type: chartTypes[2], unit: UNIT_PERCENTAGE, namesItems: [{ name: 'disk_used_perc' }] },
  { title: 'Swap Usage', type: chartTypes[2], unit: UNIT_PERCENTAGE, namesItems: [{ name: 'swap_used_perc' }] }
]

const AgentSystemDashboard = () => {
  const [period, setPeriod] = useState(
    window.localStorage.getItem('GLOUTON_STORAGE_period')
      ? JSON.parse(window.localStorage.getItem('GLOUTON_STORAGE_period'))
      : { minutes: 60 }
  )
  const [showEditPeriodMal, setShowEditPeriodMal] = useState(false)
  useEffect(() => {
    document.title = 'Dashboard | Glouton'
  }, [])

  useEffect(() => {
    window.localStorage.setItem('GLOUTON_STORAGE_period', JSON.stringify(period))
  }, [period])

  let periodText = ''
  if (period.minutes) {
    const range = lastQuickRanges.find(p => p.value === period.minutes)
    if (range) periodText = range.label
  } else if (period.from && period.to) {
    periodText = 'from ' + formatDateTime(period.from) + ' to ' + formatDateTime(period.to)
  }

  const onPeriodClick = () => setShowEditPeriodMal(true)

  const handleBackwardForwardFunc = (isForward = false) => {
    let startDate = new Date()
    let endDate = new Date()
    if (period.minutes) {
      startDate.setUTCMinutes(startDate.getUTCMinutes() - period.minutes)
      const res = computeBackwardForward(period.minutes, startDate, endDate, isForward)
      startDate = res.startDate
      endDate = res.endDate
    } else if (period.from && period.to) {
      const nbMinutes = Math.floor(Math.abs(new Date(period.to) - new Date(period.from)) / 1000 / 60)
      startDate = new Date(period.from)
      endDate = new Date(period.to)
      const res = computeBackwardForward(nbMinutes, startDate, endDate, isForward)
      startDate = res.startDate
      endDate = res.endDate
    }
    setPeriod({ from: startDate, to: endDate })
  }

  let editFromAndToModal = null
  if (showEditPeriodMal) {
    editFromAndToModal = (
      <EditPeriodModal
        period={period}
        onPeriodChange={newPeriod => setPeriod(newPeriod)}
        onClose={() => setShowEditPeriodMal(false)}
      />
    )
  }

  let refetchTime = 10080
  if (period.minutes) {
    refetchTime = period.minutes / 6
  }

  return (
    <>
      {editFromAndToModal}
      <div className="row">
        <div className="col-xl-12">
          <div className="btn-toolbar float-right" id="copy">
            <button className="btn btn-outline-dark" onClick={onPeriodClick}>
              {periodText}
            </button>
          </div>
        </div>
      </div>
      <div className="marginOffset">
        <div className="row">
          {gaugesBar.map(gaugeItem => (
            <div className="col-sm-3" key={gaugeItem.title}>
              <VisibilitySensor
                partialVisibility
                offset={{ top: -460, bottom: -460 }}
                scrollCheck
                intervalCheck
                intervalDelay={10000}
                resizeCheck
              >
                {renderProps => {
                  if (renderProps.isVisible) {
                    return (
                      <WidgetDashboardItem
                        type={chartTypes[0]}
                        title={gaugeItem.title}
                        namesItems={[{ name: gaugeItem.name, item: gaugeItem.item }]}
                        unit={gaugeItem.unit}
                        refetchTime={refetchTime}
                        period={period}
                        isVisible={renderProps.isVisible}
                      />
                    )
                  } else {
                    return <MetricGaugeItem title={gaugeItem.title} loading />
                  }
                }}
              </VisibilitySensor>
            </div>
          ))}
          {widgets.map(widget => (
            <div className="col-sm-12" style={{ marginTop: '1rem' }} key={widget.title}>
              <VisibilitySensor
                partialVisibility
                offset={{ top: 20, bottom: 20 }}
                scrollCheck
                intervalCheck
                intervalDelay={10000}
                resizeCheck
              >
                {renderProps => {
                  if (renderProps.isVisible) {
                    return (
                      <WidgetDashboardItem
                        type={widget.type}
                        title={widget.title}
                        refetchTime={refetchTime}
                        period={period}
                        unit={widget.unit}
                        namesItems={widget.namesItems}
                        handleBackwardForward={handleBackwardForwardFunc}
                      />
                    )
                  } else {
                    return <LineChart title={widget.title} loading />
                  }
                }}
              </VisibilitySensor>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}

AgentSystemDashboard.propTypes = {}

export default AgentSystemDashboard
