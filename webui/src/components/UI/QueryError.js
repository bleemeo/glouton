import React from 'react'
import PropTypes from 'prop-types'
import Panel from './Panel'
import FaIcon from './FaIcon'
import A from './A'

const QueryError = ({ error }) => {
  return (
    <Panel>
      <div className="d-flex flex-column justify-content-end align-items-center" style={{ height: '8rem' }}>
        <h3>{error.toString()}</h3>
        <p style={{ fontSize: '110%' }}>
          Press F5 or{' '}
          <A onClick={() => window.location.reload()}>
            <FaIcon icon="fa fa-sync" />
          </A>{' '}
          to reload the page
        </p>
      </div>
    </Panel>
  )
}

QueryError.propTypes = {
  error: PropTypes.object.isRequired
}

export default QueryError
