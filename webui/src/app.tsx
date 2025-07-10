import { Suspense } from "react";
import { createRoot } from "react-dom/client";
import PanelErrorBoundary from "./components/UI/PanelErrorBoundary";
import PanelLoading from "./components/UI/PanelLoading";
import Root from "./components/Root";

import "core-js/es/object";
import "core-js/es/object/values";
import "core-js/es/object/entries";

// @ts-expect-error ts(2307)
import "./styles/bootstrap.scss";

const container = document.getElementById("main");
const root = createRoot(container!);

root.render(
  <PanelErrorBoundary>
    <Suspense fallback={<PanelLoading />}>
      <Root />
    </Suspense>
  </PanelErrorBoundary>,
);
