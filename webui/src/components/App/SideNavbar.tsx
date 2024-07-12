import React, { FC, Suspense } from "react";
import { Link } from "react-router-dom";

import Fallback from "../UI/Fallback";
import PanelErrorBoundary from "../UI/PanelErrorBoundary";

const SideNavBar: FC = () => (
  <PanelErrorBoundary>
    <Suspense fallback={<Fallback />}>
      <>
        <nav
          className="navbar-fixed-side-left fixed-top"
          style={{ height: "115%" }}
        >
          <ul
            className="navbar-nav"
            style={{
              width: "100%",
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
            }}
          >
            <Link className="navbar-brand mr-auto" to="/">
              <li className="nav-item">
                <img
                  src="/static/img/favicon.png"
                  style={{ height: "2.2rem" }}
                />
              </li>
            </Link>
            <Link
              className="navbar-brand mr-auto"
              to="/dashboard"
              title="Dashboard"
            >
              <li className="nav-item">
                <img
                  src="/static/img/tachometer.png"
                  style={{ height: "1.7rem" }}
                />
              </li>
            </Link>
            <Link className="navbar-brand mr-auto" to="/docker" title="Docker">
              <li className="nav-item">
                <img
                  src="/static/img/docker-brands.png"
                  style={{ height: "1.7rem" }}
                />
              </li>
            </Link>
            <Link
              className="navbar-brand mr-auto"
              to="/processes"
              title="Processes"
            >
              <li className="nav-item">
                <img
                  src="/static/img/processes.png"
                  style={{ height: "1.7rem" }}
                />
              </li>
            </Link>
            <Link
              className="navbar-brand mr-auto"
              to="/informations"
              title="Informations"
            >
              <li className="nav-item">
                <img
                  src="/static/img/info-circle-solid.png"
                  style={{ height: "1.7rem" }}
                />
              </li>
            </Link>
          </ul>
        </nav>
      </>
    </Suspense>
  </PanelErrorBoundary>
);

export default SideNavBar;
