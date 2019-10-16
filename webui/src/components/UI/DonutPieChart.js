import d3 from 'd3'
import React from 'react'
import PropTypes from 'prop-types'
import Tooltip from 'react-tooltip'

const colorScale = d3.scale.category20()

const tau = Math.PI * 2
const size = 100

const radius = size / 2
const innerRadius = radius - radius / 2
const strokeWidth = radius / 5
const outerRadius = radius - strokeWidth

const backgroundArc = d3.svg
  .arc()
  .innerRadius(innerRadius)
  .outerRadius(outerRadius)
  .startAngle(0)
  .endAngle(tau)

const DonutPieChart = ({ values, fontSize, valueFormatter }) => {
  let arcCmpts = []
  let totalValuesTau = 0.0
  let textCmpt = null
  const id = Math.floor(Math.random() * 1024) + 1
  values.forEach((item, idx) => {
    if (typeof item.value === 'number' && !isNaN(item.value)) {
      const x = (item.value * tau) / 100
      const arc = d3.svg
        .arc()
        .innerRadius(innerRadius)
        .outerRadius(outerRadius)
        .startAngle(totalValuesTau)
        .endAngle(totalValuesTau + x)
      arcCmpts.push(
        <path key={idx.toString()} style={{ fill: `${item.color ? '#' + item.color : colorScale(idx)}` }} d={arc()} />
      )
      totalValuesTau += x
    }
  })

  if (values.length === 1) {
    textCmpt = (
      <text
        fontSize={`${fontSize}`}
        textAnchor="middle"
        fill="currentColor"
        style={{ dominantBaseline: 'middle', fontWeight: 'bold' }}
      >
        {valueFormatter(values[0].value)}
      </text>
    )
  }

  return (
    <div className="d-flex justify-content-center">
      <svg
        className="opacityTransition"
        viewBox={`0 0 ${size} ${size}`}
        width="68%"
        height="68%"
        data-tip
        data-for={id.toString()}
      >
        <g transform={`translate(${radius},${radius})`}>
          <path className="gaugeBg" d={backgroundArc()} />
          {arcCmpts.map(arcCmpt => arcCmpt)}
          {textCmpt}
        </g>
      </svg>
      {values.length > 1 ? (
        <Tooltip id={id.toString()} effect="solid">
          {values.map((v, idx) => (
            <div key={idx.toString()}>
              <span style={{ color: v.color ? '#' + v.color : colorScale(idx), fontSize: '110%' }}>
                {v.value.toFixed(2) + '%'} <b>{v.label}</b>
              </span>
            </div>
          ))}
        </Tooltip>
      ) : null}
    </div>
  )
}

DonutPieChart.propTypes = {
  values: PropTypes.instanceOf(Array),
  fontSize: PropTypes.number,
  valueFormatter: PropTypes.func.isRequired
}

DonutPieChart.defaultProps = {
  fontSize: 19.5
}

export default DonutPieChart
