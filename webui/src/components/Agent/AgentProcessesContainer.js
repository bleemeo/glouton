import React, { useEffect } from "react";

import AgentProcesses from "./AgentProcesses";
import Loading from "../UI/Loading";
import Panel from "../UI/Panel";
import QueryError from "../UI/QueryError";
import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";
import FetchSuspense from "../UI/FetchSuspense";
import { PROCESSES_URL } from "../utils/dataRoutes";

const AgentProcessesContainer = () => {
  useEffect(() => {
    document.title = "Processes | Glouton";
  }, []);

  const { isLoading, isUpdating, error, data } = useHTTPDataFetch(PROCESSES_URL, null, 10000);
  const processes = data;

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
    >
      {({ processes }) => (
        <div style={{ marginTop: "1.5rem" }}>
          <Panel>
            <AgentProcesses top={processes} sizePage={20} />
          </Panel>
          {isUpdating && (
            <div className="d-flex justify-content-center align-items-center">
              <Loading size="sm" />
            </div>
          )}
        </div>
      )}
    </FetchSuspense>
  );
};

export default AgentProcessesContainer;
