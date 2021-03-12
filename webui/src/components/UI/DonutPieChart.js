import * as d3 from 'd3'
import React from 'react'
import PropTypes from 'prop-types'

const tau = Math.PI * 1.6
const size = 100

const radius = size / 2
const innerRadius = radius - radius / 2
const strokeWidth = radius / 3
const outerRadius = radius - strokeWidth

const backgroundArc = d3.arc().innerRadius(innerRadius).outerRadius(outerRadius).startAngle(0).endAngle(tau)

const createArcPath = (x, total) => {
  return d3
    .arc()
    .innerRadius(innerRadius)
    .outerRadius(outerRadius)
    .startAngle(total)
    .endAngle(total + x)
}

const DonutPieChart = ({ value, segmentsColor, segmentsStep, fontSize, formattedValue }) => {
  const arcCmpts = []
  let totalValuesTau = 0.0
  let previousStep = 0
  let textCmpt = null
  // segmentsStep indicates the number of steps that compose gauge
  // For example, if you have [25, 50, 100]
  // That you have a path from 0 to 25, a path from 25 to 50 and a path from 50 to 100
  segmentsStep.forEach((item, idx) => {
    if (typeof item === 'number' && !isNaN(item)) {
      const tauValues = []
      // These conditions are here to give transparency for each path
      if (value > previousStep) {
        if (value < item) {
          // We have to build two paths if the value is between two steps
          tauValues.push({
            value: ((value - previousStep) * tau) / 100,
            isTransparent: false
          })
          tauValues.push({
            value: ((item - value) * tau) / 100,
            isTransparent: true
          })
        } else {
          tauValues.push({
            value: ((item - previousStep) * tau) / 100,
            isTransparent: false
          })
        }
      } else {
        tauValues.push({
          value: ((item - previousStep) * tau) / 100,
          isTransparent: true
        })
      }
      tauValues.forEach((tauValue, idxTau) => {
        const arc = createArcPath(tauValue.value, totalValuesTau)
        const opacity = tauValue.isTransparent ? '55' : 'ff'
        arcCmpts.push(
          <path key={idx.toString() + idxTau.toString()} style={{ fill: segmentsColor[idx] + opacity }} d={arc()} />
        )
        totalValuesTau += tauValue.value
      })
      previousStep = item
    }
  })
  if (typeof value === 'number' && !isNaN(value)) {
    textCmpt = (
      <text
        fontSize={`${typeof InstallTrigger !== 'undefined' ? fontSize - 3 : fontSize}`}
        textAnchor="middle"
        style={{
          dominantBaseline: 'middle',
          fontWeight: 'bold',
          fill: '#000'
        }}
        transform="rotate(144)"
      >
        {formattedValue}
      </text>
    )
  }

  return (
    <div className="block-fullsize d-flex justify-content-center">
      <svg
        className="opacityTransition"
        viewBox={`0 0 ${size} ${size - 10}`}
        width="100%"
        height="100%"
        style={{ display: 'block' }}
      >
        <g transform={`translate(${radius},${radius}) scale(1.5) rotate(216)`}>
          <path className="gaugeBg" d={backgroundArc()} />
          {arcCmpts.map(arcCmpt => arcCmpt)}
          {textCmpt}
        </g>
      </svg>
    </div>
  )
}

DonutPieChart.propTypes = {
  value: PropTypes.number,
  segmentsColor: PropTypes.array.isRequired,
  segmentsStep: PropTypes.array.isRequired,
  fontSize: PropTypes.number.isRequired,
  formattedValue: PropTypes.string.isRequired
}

DonutPieChart.defaultProps = {
  color: '#467FCF',
  fontSize: 19.5
}

export default DonutPieChart
