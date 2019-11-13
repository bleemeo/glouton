import React from 'react'
import PropTypes from 'prop-types'
import QueryError from './QueryError'
import Loading from './Loading'

const FetchSuspense = ({ children, isLoading, error, loadingComponent, fallbackComponent, ...other }) => {
  if (isLoading) {
    return loadingComponent || <Loading size="xl" />
  } else if (error) {
    return fallbackComponent || <QueryError />
  } else {
    return children({ ...other })
  }
}

FetchSuspense.propTypes = {
  children: PropTypes.func.isRequired,
  isLoading: PropTypes.bool.isRequired,
  error: PropTypes.oneOfType([PropTypes.object, PropTypes.bool]),
  loadingComponent: PropTypes.element,
  fallbackComponent: PropTypes.element
}

export default FetchSuspense
