import React, { useRef } from 'react'
import PropTypes from 'prop-types'
import { gql } from 'apollo-boost'
import MetricGaugeItem from '../Metric/MetricGaugeItem'
import { chartTypes, computeEnd, composeMetricName } from '../utils'
import LineChart from './LineChart'
import { useFetch, POLL } from '../utils/hooks'

const CPU = ['cpu_steal', 'cpu_softirq', 'cpu_interrupt', 'cpu_system', 'cpu_user', 'cpu_nice', 'cpu_wait', 'cpu_idle']
const MEMORY = ['mem_used', 'mem_buffered', 'mem_cached', 'mem_free']

const GET_POINTS = gql`
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
const WidgetDashboardItem = ({
  type,
  title,
  namesItems,
  unit,
  period,
  refetchTime,
  isVisible,
  handleBackwardForward
}) => {
  const previousError = useRef(null)
  const handleBackwardForwardFunc = (isForward = false) => {
    handleBackwardForward(isForward)
  }

  let metricsFilter = []
  switch (type) {
    case chartTypes[1]:
      if (title === 'Processor Usage') {
        CPU.forEach(name => {
          metricsFilter.push({ labels: [{ key: '__name__', value: name }] })
        })
      } else if (title === 'Memory Usage') {
        MEMORY.forEach(name => {
          metricsFilter.push({ labels: [{ key: '__name__', value: name }] })
        })
      }
      break
    default:
      namesItems.forEach(nameItem => {
        metricsFilter.push({ labels: [{ key: '__name__', value: nameItem.name }] })
        if (nameItem.item) metricsFilter.push({ labels: [{ key: 'item', value: nameItem.item }] })
      })
  }
  const { isLoading, error, points, networkStatus } = useFetch(
    GET_POINTS,
    {
      metricsFilter,
      start: period.from ? new Date(period.from).toISOString() : '',
      end: period.to ? new Date(period.to).toISOString() : '',
      minutes: period.minutes ? period.minutes : 0
    },
    refetchTime * 1000
  )
  let hasError = error
  if (previousError.current && !error && networkStatus === POLL) {
    hasError = previousError.current
  }
  let displayWidgetItem
  if (isLoading || !points) {
    switch (type) {
      case chartTypes[0]:
        displayWidgetItem = <MetricGaugeItem loading name={title} />
        break
      default:
        displayWidgetItem = <LineChart title={title} loading />
        break
    }
  } else if (hasError) {
    switch (type) {
      case chartTypes[0]:
        displayWidgetItem = <MetricGaugeItem hasError={hasError} name={title} />
        break
      default:
        displayWidgetItem = <LineChart title={title} hasError={hasError} />
        break
    }
  } else {
    switch (type) {
      case chartTypes[0]:
        const resultGauge = points.sort((a, b) =>
          a.labels.find(l => l.key === 'item').value.localeCompare(b.labels.find(l => l.key === 'item').value)
        )[0]
        const end = computeEnd(type, period)
        let lastPoint = null
        let thresholds = null
        if (
          resultGauge &&
          resultGauge.points &&
          new Date(resultGauge.points[resultGauge.points.length - 1].time) <= new Date(end)
        ) {
          lastPoint = resultGauge.points[resultGauge.points.length - 1].value
          thresholds = resultGauge.thresholds
        }
        displayWidgetItem = <MetricGaugeItem unit={unit} value={lastPoint} thresholds={thresholds} name={title} />
        break
      case chartTypes[1]:
        const resultStacked = points
        displayWidgetItem = (
          <LineChart
            stacked
            metrics={resultStacked}
            title={title}
            unit={unit}
            period={period}
            refetchTime={refetchTime}
            handleBackwardForward={handleBackwardForwardFunc}
          />
        )
        break
      case chartTypes[2]:
        const resultsLines = points
        resultsLines.sort((a, b) => {
          const aLabel = composeMetricName(a)
          const bLabel = composeMetricName(b)
          return aLabel.nameDisplay.localeCompare(bLabel.nameDisplay)
        })
        displayWidgetItem = (
          <LineChart
            metrics={resultsLines}
            title={title}
            unit={unit}
            period={period}
            refetchTime={refetchTime}
            handleBackwardForward={handleBackwardForwardFunc}
          />
        )
        break
    }
  }
  previousError.current = error
  return (
    <div>
      {/* See Issue : https://github.com/apollographql/apollo-client/pull/4974 */}
      {displayWidgetItem}
    </div>
  )
}

WidgetDashboardItem.propTypes = {
  type: PropTypes.string.isRequired,
  title: PropTypes.string.isRequired,
  namesItems: PropTypes.instanceOf(Array),
  unit: PropTypes.number,
  refetchTime: PropTypes.number.isRequired,
  period: PropTypes.object.isRequired,
  isVisible: PropTypes.bool,
  handleBackwardForward: PropTypes.func
}

export default React.memo(
  WidgetDashboardItem,
  (prevProps, nextProps) => prevProps.period === nextProps.period && prevProps.isVisible === nextProps.isVisible
)
