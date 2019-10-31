import React, { useState } from 'react'
import PropTypes from 'prop-types'
import { Collapse } from 'react-collapse'

import MetricNumberItem from '../Metric/MetricNumberItem'
import MetricGaugeItem from '../Metric/MetricGaugeItem'
import FaIcon from '../UI/FaIcon'
import Panel from '../UI/Panel'
import { colorForStatus } from '../utils/converter'

const WidgetItem = ({
  metrics,
  title,
  shouldBeExpanded,
  regexp,
  isGaugeOnly,
  excludeMetrics = [],
  includeMetrics = []
}) => {
  const [expanded, setexpanded] = useState(shouldBeExpanded ? true : false)
  let metricsToConcat = []
  if (includeMetrics.length) {
    metricsToConcat = metrics.filter(m => includeMetrics.some(meRegexp => meRegexp.test(m.label)))
  }
  return (
    <Panel>
      <div className="marginOffset">
        <a onClick={() => setexpanded(!expanded)}>
          <FaIcon icon={expanded ? 'fa fa-caret-down' : 'fa fa-caret-right'} />
          <b style={{ fontSize: 26 }}> {title}</b>
        </a>
        <Collapse isOpened={expanded}>
          <div className="row">
            {metrics
              .filter(
                m => regexp.test(m.label) && !m.label.includes('_status') && !excludeMetrics.some(me => m.label === me)
              )
              .concat(metricsToConcat)
              .map((metric, idx) => {
                if (/_perc$/.test(metric.label) || isGaugeOnly) {
                  const metricStatus = metrics.find(m => m.label === metric.label + '_status')
                  return (
                    <div className="col-2" key={idx.toString()} style={{ marginBottom: '0.8rem' }}>
                      <MetricGaugeItem
                        name={
                          metric.item ? (
                            <div>
                              {metric.label} <br /> {metric.item}
                            </div>
                          ) : (
                            metric.label
                          )
                        }
                        unit={1}
                        value={metric.value}
                        status={metricStatus ? metricStatus.value : 0}
                        titleFontSize={22}
                      />
                    </div>
                  )
                }
                return (
                  <div className="col-2" key={idx.toString()}>
                    <MetricNumberItem
                      title={
                        metric.item ? (
                          <div>
                            {metric.label} <br /> {metric.item}
                          </div>
                        ) : (
                          metric.label
                        )
                      }
                      value={metric.value}
                      titleFontSize={22}
                    />
                  </div>
                )
              })}
          </div>
        </Collapse>
      </div>
    </Panel>
  )
}

WidgetItem.propTypes = {
  metrics: PropTypes.instanceOf(Array).isRequired,
  title: PropTypes.string.isRequired,
  regexp: PropTypes.object.isRequired,
  shouldBeExpanded: PropTypes.bool,
  isGaugeOnly: PropTypes.bool,
  excludeMetrics: PropTypes.instanceOf(Array),
  includeMetrics: PropTypes.instanceOf(Array)
}

export default WidgetItem
