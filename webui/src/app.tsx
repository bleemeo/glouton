import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";

import { AppLayout } from "./app/AppLayout";
import { AppRoutes } from "./app/AppRoutes";
import { ErrorBoundary } from "./app/ErrorBoundary";
import { Provider } from "./app/Provider";

const container = document.getElementById("main");

if (!container) {
  throw new Error("Glouton panel mount target #main is missing from index.html");
}

createRoot(container).render(
  <Provider>
    <ErrorBoundary>
      <BrowserRouter>
        <AppLayout>
          <AppRoutes />
        </AppLayout>
      </BrowserRouter>
    </ErrorBoundary>
  </Provider>,
);
