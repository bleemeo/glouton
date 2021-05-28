import React, { useEffect } from "react";

import AgentProcesses from "./AgentProcesses";
import Loading from "../UI/Loading";
import Panel from "../UI/Panel";
import QueryError from "../UI/QueryError";
import { useFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";
import FetchSuspense from "../UI/FetchSuspense";
import { PROCESSES } from "../utils/gqlRequests";

const AgentProcessesContainer = () => {
  useEffect(() => {
    document.title = "Processes | Glouton";
  }, []);

  const { isLoading, error, points, processes } = useFetch(
    PROCESSES,
    null,
    10000
  );
  return (
    <FetchSuspense
      isLoading={isLoading}
      error={error || isNullOrUndefined(processes)}
      loadingComponent={
        <div className="marginOffset d-flex justify-content-center align-items-center">
          <Loading size="xl" />
        </div>
      }
      fallbackComponent={<QueryError noBorder />}
      processes={processes}
      points={points}
    >
      {({ processes }) => {
        return (
          <div style={{ marginTop: "1.5rem" }}>
            <Panel>
              <AgentProcesses top={processes} sizePage={20} />
            </Panel>
          </div>
        );
      }}
    </FetchSuspense>
  );
};

export default AgentProcessesContainer;
