/* eslint-disable camelcase */
import d3 from 'd3'
import React, { useState } from 'react'
import PropTypes from 'prop-types'
import { Grid } from 'tabler-react'
import { gql } from 'apollo-boost'
import ReactTooltip from 'react-tooltip'
import Panel from '../UI/Panel'
import QueryError from '../UI/QueryError'
import Loading from '../UI/Loading'
import { cssClassForStatus, textForStatus } from '../utils/converter'
import FaIcon from '../UI/FaIcon'
import ServiceDetailsModal from '../Service/ServiceDetailsModal'
import { useFetch } from '../utils/hooks'
import { formatDateTimeWithSeconds } from '../utils/formater'
import Smiley from '../UI/Smiley'
import { Problems, isNullOrUndefined } from '../utils'
import FetchSuspense from '../UI/FetchSuspense'

const AGENT_DETAILS = gql`
  query agent_details {
    services(isActive: true) {
      name
      containerId
      ipAddress
      listenAddresses
      exePath
      active
      status
      statusDescription
    }
    agentInformation {
      registrationAt
      lastReport
      isConnected
    }
    tags {
      tagName
    }
    agentStatus {
      status
      statusDescription
    }
  }
`

const AgentDetails = ({ facts }) => {
  const [showServiceDetails, setShowServiceDetails] = useState(null)

  const factUpdatedAt = facts.find(f => f.name === 'fact_updated_at').value
  let factUpdatedAtDate
  if (factUpdatedAt) {
    factUpdatedAtDate = new Date(factUpdatedAt)
  }

  let expireAgentBanner = null
  let agentDate = null
  const agentVersion = facts.find(f => f.name === 'glouton_version').value
  if (agentVersion) {
    const expDate = new Date()
    expDate.setDate(expDate.getDate() - 60)
    // First try new format (e.g. 18.03.21.134432)
    agentDate = d3.time.format('%y.%m.%d.%H%M%S').parse(agentVersion.slice(0, 15))
    if (!agentDate) {
      // then old format (0.20180321.134432)
      agentDate = d3.time.format('0.%Y%m%d.%H%M%S').parse(agentVersion.slice(0, 17))
    }
    if (agentDate && agentDate < expDate) {
      expireAgentBanner = (
        <div className="row">
          <div className="col-xl-12">
            <div className="alert alert-danger" role="alert">
              This agent is more than 60 days old. You should update it. See the
              <a href="https://docs.bleemeo.com/agent/upgrade-agent/" target="_blank">
                {' '}
                documentation
              </a>
              &nbsp; to learn how to do it.
            </div>
          </div>
        </div>
      )
    }
  }

  let serviceDetailsModal = null
  if (showServiceDetails) {
    serviceDetailsModal = (
      <ServiceDetailsModal service={showServiceDetails} closeAction={() => setShowServiceDetails(null)} />
    )
  }

  const { isLoading, error, services, tags, agentInformation, agentStatus } = useFetch(AGENT_DETAILS, null, 60000)
  let tooltipType = 'info'
  let problems = null
  if (agentStatus) {
    switch (agentStatus.status) {
      case 0:
        tooltipType = 'success'
        break
      case 1:
        tooltipType = 'warning'
        break
      case 2:
        tooltipType = 'error'
        break
      default:
        tooltipType = 'info'
        break
    }

    problems = (
      <div className="marginOffset">
        <p className="text-center" data-tip data-for="agentStatus">
          <Smiley status={agentStatus.status} />
        </p>
        <p className="text-center">{textForStatus(agentStatus.status)}</p>
        {agentStatus.statusDescription.length > 0 ? (
          <ReactTooltip place="bottom" id="agentStatus" effect="solid" type={tooltipType}>
            <div style={{ maxWidth: '80rem', wordBreak: 'break-all' }}>
              <Problems problems={agentStatus.statusDescription} />
            </div>
          </ReactTooltip>
        ) : null}
      </div>
    )
  }

  return (
    <div id="page-wrapper" style={{ marginTop: '1.5rem' }}>
      {serviceDetailsModal}

      {expireAgentBanner}

      <Grid.Row>
        <Grid.Col xl={4}>
          <FetchSuspense
            isLoading={isLoading}
            error={
              error ||
              isNullOrUndefined(services) ||
              isNullOrUndefined(tags) ||
              isNullOrUndefined(agentInformation) ||
              isNullOrUndefined(agentStatus)
            }
            services={services}
          >
            {({ services }) => (
              <Panel>
                <div className="marginOffset">
                  Services running on this agent:
                  <ul className="list-inline">
                    {services
                      .filter(service => service.active)
                      .sort((a, b) => a.name.localeCompare(b.name))
                      .map((service, idx) => {
                        return (
                          <li key={idx} className="list-inline-item">
                            <button
                              // TODO : Change in favor of service status
                              className={`btn blee-label btn-${cssClassForStatus(service.status)}`}
                              onClick={() => setShowServiceDetails(service)}
                            >
                              {service.name}
                              &nbsp;
                              <FaIcon icon="fas fa-info-circle" />
                            </button>
                          </li>
                        )
                      })}
                  </ul>
                </div>
              </Panel>
            )}
          </FetchSuspense>
          {agentInformation ? (
            <Panel>
              <div className="marginOffset">
                <ul className="list-unstyled">
                  {agentInformation.registrationAt && new Date(agentInformation.registrationAt).getFullYear() !== 1 ? (
                    <li>
                      <b>Glouton registration at:</b> {formatDateTimeWithSeconds(agentInformation.registrationAt)}
                    </li>
                  ) : null}
                  {agentInformation.lastReport && new Date(agentInformation.lastReport).getFullYear() !== 1 ? (
                    <li>
                      <b>Glouton last report:</b> {formatDateTimeWithSeconds(agentInformation.lastReport)}
                    </li>
                  ) : null}
                  <li>
                    <div className="d-flex flex-row align-items-center">
                      <b style={{ marginRight: '0.4rem' }}>Is connected to Bleemeo ?</b>
                      <div className={agentInformation.isConnected ? 'isConnected' : 'isNotConnected'} />
                    </div>
                  </li>
                </ul>
              </div>
            </Panel>
          ) : null}
        </Grid.Col>
        <Grid.Col x={4}>
          <FetchSuspense
            isLoading={isLoading}
            error={
              error ||
              isNullOrUndefined(services) ||
              isNullOrUndefined(tags) ||
              isNullOrUndefined(agentInformation) ||
              isNullOrUndefined(agentStatus)
            }
            tags={tags}
          >
            {({ tags }) => (
              <Panel>
                <div className="marginOffset">
                  Tags for {facts.find(f => f.name === 'fqdn').value}:
                  {tags.length > 0 ? (
                    <ul className="list-inline">
                      {tags.map(tag => (
                        <li key={tag.tagName} className="list-inline-item">
                          <span className="badge badge-info blee-label">{tag.tagName}</span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <h4>There is no tag to display</h4>
                  )}
                </div>
              </Panel>
            )}
          </FetchSuspense>
          <Grid.Row>
            <Grid.Col xl={6} offset={3}>
              {problems ? <Panel>{problems}</Panel> : null}
            </Grid.Col>
          </Grid.Row>
        </Grid.Col>
        <Grid.Col xl={4}>
          <Panel>
            <div className="marginOffset">
              <h5>Information retrieved from the agent</h5>
              {factUpdatedAtDate ? <p>(last update: {factUpdatedAtDate.toLocaleString()})</p> : null}
              <ul className="list-unstyled">
                {facts
                  .filter(f => f.name !== 'fact_updated_at')
                  .sort((a, b) => a.name.localeCompare(b.name))
                  .map(fact => (
                    <li key={fact.name}>
                      <b>{fact.name}:</b> {fact.value}
                    </li>
                  ))}
              </ul>
            </div>
          </Panel>
        </Grid.Col>
      </Grid.Row>
    </div>
  )
}

AgentDetails.propTypes = {
  facts: PropTypes.instanceOf(Array).isRequired
}

export default AgentDetails
