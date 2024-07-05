import React, { lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import PanelErrorBoundary from "./components/UI/PanelErrorBoundary";
import PanelLoading from "./components/UI/PanelLoading";

import "core-js/es/object";
import "core-js/es/object/values";
import "core-js/es/object/entries";

import "./styles/bootstrap.scss";
import "tabler-react/dist/Tabler.css";

const App = lazy(() => import("./components/Root"));

const container = document.getElementById("main");
const root = createRoot(container);

root.render(
  <PanelErrorBoundary>
    <Suspense fallback={<PanelLoading />}>
      <App />
    </Suspense>
  </PanelErrorBoundary>,
);
