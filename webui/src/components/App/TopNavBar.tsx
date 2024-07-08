import React from "react";
import { useHTTPDataFetch } from "../utils/hooks";
import FetchSuspense from "../UI/FetchSuspense";
import { FACTS_URL } from "../utils/dataRoutes";
import { Fact } from "../Data/data.interface";

const TopNavBar = () => {
  const {
    isLoading,
    error,
    data: facts,
  } = useHTTPDataFetch<Fact[]>(FACTS_URL, null, 10000);

  return (
    <nav
      className="navbar navbar-expand-lg
  navbar-light bg-light navbar-fixed-top fixed-top
  navbar-toggleable-md justify-content-end"
    >
      <FetchSuspense isLoading={isLoading} error={error} facts={facts}>
        {({ facts }) => (
          <h2 style={{ marginBlockEnd: "0rem", marginLeft: "1rem" }}>
            {facts.find((f: Fact) => f.name === "fqdn").value}
          </h2>
        )}
      </FetchSuspense>
    </nav>
  );
};

export default TopNavBar;
