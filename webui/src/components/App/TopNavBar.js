import React from "react";
import { useFetch } from "../utils/hooks";
import { FACTS } from "../utils/gqlRequests";
import FetchSuspense from "../UI/FetchSuspense";

const TopNavBar = () => {
  const { isLoading, error, facts } = useFetch(FACTS);
  return (
    <nav
      className="navbar navbar-expand-lg
  navbar-light bg-light navbar-fixed-top fixed-top
  navbar-toggleable-md justify-content-end"
    >
      <FetchSuspense isLoading={isLoading} error={error} facts={facts}>
        {({ facts }) => (
          <h2 style={{ marginBlockEnd: "0rem", marginLeft: "1rem" }}>
            {facts.find((f) => f.name === "fqdn").value}
          </h2>
        )}
      </FetchSuspense>
    </nav>
  );
};

export default TopNavBar;
