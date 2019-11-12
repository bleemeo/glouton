import React from 'react'
import PropTypes from 'prop-types'
import QueryError from './QueryError'
import Loading from './Loading'

const FetchSuspense = ({ children, isLoading, error, loadingComponent, fallbackComponent }) => {
  if (isLoading) {
    return loadingComponent || <Loading size="xl" />
  } else if (error) {
    return fallbackComponent || <QueryError />
  } else {
    return children
  }
}

FetchSuspense.propTypes = {
  children: PropTypes.element.isRequired,
  isLoading: PropTypes.boolean.isRequired,
  error: PropTypes.object,
  loadingComponent: PropTypes.element,
  fallbackComponent: PropTypes.element
}

export default FetchSuspense
