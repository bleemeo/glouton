import React from 'react'
import PropTypes from 'prop-types'

import FaIcon from './FaIcon'
import A from './A'

class PanelErrorBoundary extends React.Component {
  static propTypes = {
    children: PropTypes.any
  }

  constructor(props) {
    super(props)
    this.state = {
      hasError: false
    }
  }
  /* eslint-disable handle-callback-err */
  static getDerivedStateFromError(error) {
    return { hasError: true }
  }
  /* eslint-enable handle-callback-err */

  render() {
    if (this.state.hasError) {
      return (
        <div
          style={{
            display: 'flex',
            flexFlow: 'column wrap',
            justifyContent: 'space-between',
            alignItems: 'center'
          }}
        >
          <div
            style={{
              flexShrink: 1,
              display: 'flex',
              flexFlow: 'column wrap',
              justifyContent: 'space-between',
              alignItems: 'center'
            }}
          >
            <h1 style={{ padding: '3rem 0', fontSize: '600%', fontWeight: 'bold' }}>An error has just happened</h1>
            <p style={{ fontSize: '140%' }}>
              Please reload the page by clicking this icon{' '}
              <A onClick={() => window.location.reload()}>
                <FaIcon icon="fas fa-sync" />
              </A>{' '}
              or by pressing F5
            </p>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

export default PanelErrorBoundary
