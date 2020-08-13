import React, { Suspense } from 'react'
import { BrowserRouter as Router, Redirect } from 'react-router-dom'
import Fallback from '../UI/Fallback'
import PanelErrorBoundary from '../UI/PanelErrorBoundary'
import FetchSuspense from '../UI/FetchSuspense'
import { useFetch } from '../utils/hooks'
import { FACTS } from '../utils/gqlRequests'

const SideNavBar = () => {
  const { isLoading, error } = useFetch(FACTS)
  return (
    <Router>
      <PanelErrorBoundary>
        <Suspense fallback={<Fallback />}>
          <FetchSuspense isLoading={isLoading} error={error}>
            {() => (
              <>
                <nav className="navbar-fixed-side-left fixed-top" style={{ height: '115%' }}>
                  <ul
                    className="navbar-nav"
                    style={{
                      width: '100%',
                      display: 'flex',
                      flexDirection: 'column',
                      alignItems: 'center'
                    }}
                  >
                    <a className="navbar-brand mr-auto" href="/">
                      <li className="nav-item">
                        <img src="/static/img/favicon.png" style={{ height: '2.2rem' }} />
                      </li>
                    </a>
                    <a className="navbar-brand mr-auto" href="/dashboard" title="Dashboard">
                      <li className="nav-item">
                        <img src="/static/img/tachometer.png" style={{ height: '1.7rem' }} />
                      </li>
                    </a>
                    <a className="navbar-brand mr-auto" href="/docker" title="Docker">
                      <li className="nav-item">
                        <img src="/static/img/docker-brands.png" style={{ height: '1.7rem' }} />
                      </li>
                    </a>
                    <a className="navbar-brand mr-auto" href="/processes" title="Processes">
                      <li className="nav-item">
                        <img src="/static/img/server.png" style={{ height: '1.7rem' }} />
                      </li>
                    </a>
                    <a className="navbar-brand mr-auto" href="/informations" title="Informations">
                      <li className="nav-item">
                        <img src="/static/img/info-circle-solid.png" style={{ height: '1.7rem' }} />
                      </li>
                    </a>
                  </ul>
                </nav>
                <Redirect to="/dashboard" />
              </>
            )}
          </FetchSuspense>
        </Suspense>
      </PanelErrorBoundary>
    </Router>
  )
}

export default SideNavBar
