import React from 'react'
import PropTypes from 'prop-types'
import DonutPieChart from '../UI/DonutPieChart'
import { unitFormatCallback } from '../utils/formater'
import Loading from '../UI/Loading'
import QueryError from '../UI/QueryError'

const MetricGaugeItem = ({
  unit,
  values,
  name,
  style = null,
  fontSize = 15,
  titleFontSize = 30,
  loading,
  hasError
}) => {
  if (loading) {
    return (
      <div className="card card-body widgetLoading" style={style}>
        <div className="d-flex flex-column flex-nowrap justify-content-center align-items-center">
          <Loading size="xl" />
        </div>
      </div>
    )
  } else if (hasError) {
    return (
      <div className="card card-body widgetError" style={style}>
        <div className="d-flex flex-column flex-nowrap justify-content-center align-items-center">
          <QueryError noBorder style={{ textAlign: 'center' }} />
        </div>
      </div>
    )
  }
  return (
    <div className="card card-body widget" style={style}>
      <div className="d-flex flex-column flex-nowrap justify-content-center align-items-center">
        <div>
          <DonutPieChart values={values} fontSize={fontSize} valueFormatter={unitFormatCallback(unit)} />
        </div>
        <div>
          <b style={{ fontSize: titleFontSize, textOverflow: 'ellipsis' }}>{name}</b>
        </div>
      </div>
    </div>
  )
}

MetricGaugeItem.propTypes = {
  unit: PropTypes.number,
  values: PropTypes.instanceOf(Array),
  name: PropTypes.oneOfType([PropTypes.object, PropTypes.string]).isRequired,
  style: PropTypes.object,
  fontSize: PropTypes.number,
  titleFontSize: PropTypes.number,
  loading: PropTypes.bool,
  hasError: PropTypes.object
}

export default MetricGaugeItem
