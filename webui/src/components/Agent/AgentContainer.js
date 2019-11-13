import React, { lazy, Suspense, useEffect } from 'react'
import { NavLink, BrowserRouter as Router, Route, Switch, Redirect } from 'react-router-dom'
import { withRouter } from 'react-router'
import { gql } from 'apollo-boost'

import PanelErrorBoundary from '../UI/PanelErrorBoundary'
import 'rc-switch/assets/index.css'
import Fallback from '../UI/Fallback'
import FetchSuspense from '../UI/FetchSuspense'
import { useFetch } from '../utils/hooks'

const FACTS = gql`
  query facts {
    facts {
      name
      value
    }
  }
`

const ScrollToTopComponent = props => {
  useEffect(() => {
    window.scroll({
      top: 0,
      left: 0
    })
  }, [props.location.pathname])

  return props.children
}

const ScrollToTop = withRouter(ScrollToTopComponent)

const AgentSystemDashboard = lazy(() => import('./AgentSystemDashboard'))
const AgentDockerListContainer = lazy(() => import('./AgentDockerListContainer'))
const AgentProcessesContainer = lazy(() => import('./AgentProcessesContainer'))
const AgentDetails = lazy(() => import('./AgentDetails'))

const menuEntries = [
  { key: 'dashboard', label: 'Dashboard' },
  // { key: 'services', label: 'Services' },
  { key: 'docker', label: 'Docker' },
  { key: 'processes', label: 'Processes' },
  { key: 'informations', label: 'Informations' }
]

const AgentContainer = () => {
  const { isLoading, error, facts } = useFetch(FACTS)
  return (
    <Router>
      <ScrollToTop>
        <PanelErrorBoundary>
          <Suspense fallback={<Fallback />}>
            <FetchSuspense isLoading={isLoading} error={error} facts={facts}>
              {({ facts }) => (
                <>
                  <div className="row">
                    <div
                      className="col-lg-12"
                      style={{
                        flexFlow: 'row wrap',
                        justifyContent: 'flex-start',
                        alignItems: 'center'
                      }}
                    >
                      <div className="d-flex align-items-center">
                        <span className="btn-group" role="group" aria-label="Agent views" style={{ display: 'inline' }}>
                          {menuEntries.map(({ key, label }) => (
                            <NavLink
                              className={'btn btn-outline-dark'}
                              role="button"
                              activeClassName="active"
                              key={key}
                              to={`/${key}`}
                              href={`/${key}`}
                            >
                              {label}
                            </NavLink>
                          ))}
                        </span>
                        <h2 style={{ marginBlockEnd: '0rem', marginLeft: '1rem' }}>
                          {facts.find(f => f.name === 'fqdn').value}
                        </h2>
                      </div>
                    </div>
                  </div>
                  <Switch>
                    <Route exact path="/dashboard" component={AgentSystemDashboard} />
                    {facts.some(f => f.name === 'docker_version') ? (
                      <Route exact path="/docker" component={AgentDockerListContainer} />
                    ) : null}
                    <Route exact path="/processes" component={AgentProcessesContainer} />
                    <Route exact path="/informations" render={() => <AgentDetails facts={facts} />} />
                    <Redirect to="/dashboard" />
                  </Switch>
                </>
              )}
            </FetchSuspense>
          </Suspense>
        </PanelErrorBoundary>
      </ScrollToTop>
    </Router>
  )
}

export default AgentContainer
