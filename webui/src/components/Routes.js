import React, { lazy, Suspense, useEffect } from "react";
import {
  BrowserRouter as Router,
  Route,
  Routes,
  Navigate,
} from "react-router-dom";
import { useLocation } from "react-router";

import PanelErrorBoundary from "./UI/PanelErrorBoundary";
import "rc-switch/assets/index.css";
import Fallback from "./UI/Fallback";
import FetchSuspense from "./UI/FetchSuspense";
import { useFetch } from "./utils/hooks";
import { FACTS } from "./utils/gqlRequests";
import SideNavBar from "./App/SideNavbar";

const ScrollToTopComponent = (props) => {
  let location = useLocation();
  useEffect(() => {
    window.scroll({
      top: 0,
      left: 0,
    });
  }, [location.pathname]);

  return props.children;
};

const ScrollToTop = ScrollToTopComponent;

const AgentSystemDashboard = lazy(() => import("./Agent/AgentSystemDashboard"));
const AgentDockerListContainer = lazy(() =>
  import("./Agent/AgentDockerListContainer"),
);
const AgentProcessesContainer = lazy(() =>
  import("./Agent/AgentProcessesContainer"),
);
const AgentDetails = lazy(() => import("./Agent/AgentDetails"));

const MyRoutes = () => {
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
                  <Routes>
                    <Route
                      exact
                      path="/dashboard"
                      element={<AgentSystemDashboard facts={facts} />}
                    />
                    {facts.some((f) => f.name === "container_runtime") ? (
                      <Route
                        exact
                        path="/docker"
                        element={<AgentDockerListContainer />}
                      />
                    ) : null}
                    <Route
                      exact
                      path="/processes"
                      element={<AgentProcessesContainer />}
                    />
                    <Route
                      exact
                      path="/informations"
                      element={<AgentDetails facts={facts} />}
                    />
                    <Route
                      exact
                      path="/"
                      element={<Navigate to="/dashboard" />}
                    />
                  </Routes>
                </>
              )}
            </FetchSuspense>
          </Suspense>
        </PanelErrorBoundary>
      </ScrollToTop>
    </Router>
  );
};

export default MyRoutes;
