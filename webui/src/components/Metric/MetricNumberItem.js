import React from 'react'
import PropTypes from 'prop-types'

import { twoDigitsWithMetricPrefix } from '../utils/formater'

const SIZE = 100

export default class MetricNumberItem extends React.Component {
  static propTypes = {
    value: PropTypes.number.isRequired,
    title: PropTypes.oneOfType([PropTypes.object, PropTypes.string]).isRequired,
    titleFontSize: PropTypes.number
  }

  static defaultProps = {
    titleFontSize: 30
  }

  resize() {
    if (this.svgElem && this.textElem) {
      const svg = this.svgElem
      const svgCTM = svg.getScreenCTM()
      const textBBox = this.textElem.getBBox()
      // svgCTM.a is the scale ratio of the SVG
      // svg.{clientHeight|clientWidth} is 0 with FF ?? so we use the parent div
      const svgHeight = svg.parentNode.clientHeight / svgCTM.a
      const svgWidth = svg.parentNode.clientWidth / svgCTM.a
      let textHeight = textBBox.height
      if (textHeight === 0) {
        textHeight = 1
      }
      let textWidth = textBBox.width
      if (textWidth === 0) {
        textWidth = 1
      }

      const ratio = Math.min(svgHeight / textHeight, svgWidth / textWidth)
      const halfSize = SIZE / 2
      const translateRatio = -halfSize * (ratio - 1)
      // Scale the text but keep it centered :
      // https://www.safaribooksonline.com/library/view/svg-essentials/0596002238/ch05s06.html
      // -centerX*(factor-1)
      this.textElem.setAttribute('transform', `matrix(${ratio}, 0, 0, ${ratio}, ${translateRatio}, ${translateRatio})`)
    }
  }

  componentDidMount() {
    this.resize()
  }

  componentDidUpdate() {
    this.resize()
  }

  render() {
    const { value, title, titleFontSize } = this.props

    let formattedValue = twoDigitsWithMetricPrefix(value)
    // Work-around a bug with FF where the first render of the text is buggy/weird
    // The bug seems to be here because the text box doesn't have any size for the renders
    // before the value is fetched, leading to the first computation of text ratio
    // being wrong.
    // With 3 non-breaking space in the text box, its size is similar to the one
    // it will get once the proper value will be displayed.
    // It is not a perfect solution and the first number is often too big or too small!
    if (formattedValue === undefined) {
      formattedValue = '\u00A0\u00A0\u00A0'
    }

    return (
      <div className="card card-body">
        <div className="d-flex justify-content-center">
          <div className="w-100 h-100 d-flex flex-column flex-nowrap justify-content-center align-items-center">
            <div className="w-100 h-100">
              <svg viewBox={`0 0 ${SIZE} ${SIZE}`} width="100%" height="100%" ref={elem => (this.svgElem = elem)}>
                <text
                  ref={elem => (this.textElem = elem)}
                  x={SIZE / 2}
                  y={SIZE / 2}
                  textAnchor="middle"
                  fill="currentColor"
                  style={{ dominantBaseline: 'middle', fontWeight: 'bold' }}
                >
                  {formattedValue}
                </text>
              </svg>
            </div>
            <div>
              <b style={{ fontSize: titleFontSize, wordBreak: 'break-word' }}>{title}</b>
            </div>
          </div>
        </div>
      </div>
    )
  }
}
