import React, { useEffect } from "react";

import AgentProcesses from "./AgentProcesses";
import Loading from "../UI/Loading";
import Panel from "../UI/Panel";
import QueryError from "../UI/QueryError";
import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";
import FetchSuspense from "../UI/FetchSuspense";
import { PROCESSES_URL } from "../utils/dataRoutes";
import { Topinfo } from "../Data/data.interface";

const AgentProcessesContainer = () => {
  useEffect(() => {
    document.title = "Processes | Glouton";
  }, []);

  const {
    isLoading,
    isFetching,
    error,
    data: processes,
  } = useHTTPDataFetch<Topinfo>(PROCESSES_URL, null, 10000);

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
          {isFetching && (
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
