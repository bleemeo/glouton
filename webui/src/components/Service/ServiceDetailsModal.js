import React from 'react'
import PropTypes from 'prop-types'
import { gql } from 'apollo-boost'

import Modal from '../UI/Modal'
import { cssClassForStatus, textForStatus } from '../utils/converter'
import Loading from '../UI/Loading'
import QueryError from '../UI/QueryError'
import { useFetch } from '../utils/hooks'

const CONTAINER_SERVICE = gql`
  query containerService($containerId: String!) {
    containers(search: $containerId, allContainers: true) {
      containers {
        name
      }
    }
  }
`

export default class ServiceDetailsModal extends React.PureComponent {
  static propTypes = {
    actions: PropTypes.object,
    service: PropTypes.object.isRequired,
    agent: PropTypes.object.isRequired,
    closeAction: PropTypes.func.isRequired
  }

  render() {
    const { service, closeAction } = this.props

    if (!service) {
      return null
    }

    return (
      <Modal title={`${service.name}`} closeAction={closeAction} closeOnBackdropClick>
        <ServiceDetails {...this.props} />
      </Modal>
    )
  }
}

/* eslint-disable react/no-multi-comp */
const ServiceDetails = ({ service }) => {
  if (!service) {
    return null
  }

  let currentStatus = service.status

  return (
    <div className="marginOffset">
      <ul className="list-unstyled">
        <li>
          <strong>Current status : </strong>
          <span className={`badge badge-${cssClassForStatus(currentStatus ? currentStatus : undefined)}`}>
            {textForStatus(currentStatus ? currentStatus : undefined)}
          </span>
        </li>
        {service.containerId ? <ServiceContainer containerId={service.containerId} /> : null}
        {!service.listenAddresses ? null : (
          <li>
            <strong>Listen addresses:</strong> {service.listenAddresses.join('\u2003')}
          </li>
        )}
        {service.exePath ? (
          <li>
            <strong>Executable path:</strong> {service.exePath}
          </li>
        ) : null}
        {currentStatus && currentStatus !== 0 ? (
          <li>
            <strong>
              Current Problem:
              <br />
            </strong>{' '}
            {service.statusDescription}
          </li>
        ) : null}
      </ul>
    </div>
  )
}

ServiceDetails.propTypes = {
  service: PropTypes.object.isRequired
}
const ServiceContainer = ({ containerId }) => {
  const { isLoading, error, containers } = useFetch(CONTAINER_SERVICE, { containerId })
  if (isLoading) {
    return <Loading size="xl" />
  } else if (error) {
    return <QueryError />
  } else {
    if (containers.containers[0]) {
      return (
        <li>
          <strong>Docker:</strong> {containers.containers[0].name}
        </li>
      )
    } else return null
  }
}

ServiceContainer.propTypes = {
  containerId: PropTypes.string.isRequired
}
