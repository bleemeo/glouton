import { Suspense, useEffect } from "react";
import {
  BrowserRouter as Router,
  Route,
  Routes,
  Navigate,
} from "react-router-dom";
import "rc-switch/assets/index.css";

import PanelErrorBoundary from "./UI/PanelErrorBoundary";
import Fallback from "./UI/Fallback";
import FetchSuspense from "./UI/FetchSuspense";
import SideNavBar from "./App/SideNavbar";
import AgentSystemDashboard from "./Agent/AgentSystemDashboard";
import AgentDockerListContainer from "./Agent/AgentDockerListContainer";
import AgentProcessesContainer from "./Agent/AgentProcessesContainer";
import AgentDetails from "./Agent/AgentDetails";

import { useLocation } from "react-router";
import { useHTTPDataFetch } from "./utils/hooks";
import { FACTS_URL } from "./utils/dataRoutes";
import { Fact } from "./Data/data.interface";

const ScrollToTopComponent = (props) => {
  const location = useLocation();
  useEffect(() => {
    window.scroll({
      top: 0,
      left: 0,
    });
  }, [location.pathname]);

  return props.children;
};

const ScrollToTop = ScrollToTopComponent;

const MyRoutes = () => {
  const { isLoading, error, data } = useHTTPDataFetch<Fact[]>(FACTS_URL, null);
  const facts = data;
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
                      path="/dashboard"
                      element={<AgentSystemDashboard />}
                    />
                    <Route
                      path="/docker"
                      element={<AgentDockerListContainer />}
                    />
                    <Route
                      path="/processes"
                      element={<AgentProcessesContainer />}
                    />
                    <Route
                      path="/informations"
                      element={<AgentDetails facts={facts} />}
                    />
                    <Route path="/" element={<Navigate to="/dashboard" />} />
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
