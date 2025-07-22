import { FC, useEffect } from "react";

import AgentProcesses from "./AgentProcesses";
import Panel from "../UI/Panel";
import QueryError from "../UI/QueryError";
import FetchSuspense from "../UI/FetchSuspense";

import { PROCESSES_URL } from "../utils/dataRoutes";
import { Topinfo } from "../Data/data.interface";
import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";

const AgentProcessesContainer: FC = () => {
  useEffect(() => {
    document.title = "Processes | Glouton";
  }, []);

  const {
    isLoading,
    error,
    data: processes,
  } = useHTTPDataFetch<Topinfo>(PROCESSES_URL, null, 10000);

  return (
    <FetchSuspense
      isLoading={isLoading}
      error={error || isNullOrUndefined(processes)}
      fallbackComponent={<QueryError noBorder />}
      processes={processes}
    >
      {({ processes }) => (
        <>
          <Panel>
            <AgentProcesses top={processes} sizePage={20} />
          </Panel>
        </>
      )}
    </FetchSuspense>
  );
};

export default AgentProcessesContainer;
