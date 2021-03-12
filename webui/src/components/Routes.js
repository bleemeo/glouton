import React, { lazy, Suspense, useEffect } from "react";
import {
  BrowserRouter as Router,
  Route,
  Switch,
  Redirect,
} from "react-router-dom";
import { withRouter } from "react-router";

import PanelErrorBoundary from "./UI/PanelErrorBoundary";
import "rc-switch/assets/index.css";
import Fallback from "./UI/Fallback";
import FetchSuspense from "./UI/FetchSuspense";
import { useFetch } from "./utils/hooks";
import { FACTS } from "./utils/gqlRequests";
import SideNavBar from "./App/SideNavbar";

const ScrollToTopComponent = (props) => {
  useEffect(() => {
    window.scroll({
      top: 0,
      left: 0,
    });
  }, [props.location.pathname]);

  return props.children;
};

const ScrollToTop = withRouter(ScrollToTopComponent);

const AgentSystemDashboard = lazy(() => import("./Agent/AgentSystemDashboard"));
const AgentDockerListContainer = lazy(() =>
  import("./Agent/AgentDockerListContainer")
);
const AgentProcessesContainer = lazy(() =>
  import("./Agent/AgentProcessesContainer")
);
const AgentDetails = lazy(() => import("./Agent/AgentDetails"));

const Routes = () => {
  const { isLoading, error, facts } = useFetch(FACTS);
  return (
    <Router>
      <ScrollToTop>
        <PanelErrorBoundary>
          <Suspense fallback={<Fallback />}>
            <FetchSuspense isLoading={isLoading} error={error} facts={facts}>
              {({ facts }) => (
                <>
                  <SideNavBar />
                  <Switch>
                    <Route
                      exact
                      path="/dashboard"
                      component={AgentSystemDashboard}
                    />
                    {facts.some((f) => f.name === "container_runtime") ? (
                      <Route
                        exact
                        path="/docker"
                        component={AgentDockerListContainer}
                      />
                    ) : null}
                    <Route
                      exact
                      path="/processes"
                      component={AgentProcessesContainer}
                    />
                    <Route
                      exact
                      path="/informations"
                      render={() => <AgentDetails facts={facts} />}
                    />
                    <Redirect to="/dashboard" />
                  </Switch>
                </>
              )}
            </FetchSuspense>
          </Suspense>
        </PanelErrorBoundary>
      </ScrollToTop>
    </Router>
  );
};

export default Routes;
