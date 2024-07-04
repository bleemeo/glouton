import React, { useState, useEffect, useMemo, useCallback, useRef } from "react";

import Docker from "./Docker";
import Loading from "../UI/Loading";
import { formatDateTime } from "../utils/formater";
import { DebounceInput } from "react-debounce-input";
import Toggle from "../UI/Toggle";
import QueryError from "../UI/QueryError";
import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";
import { CONTAINERS_DETAILS } from "../utils/gqlRequests";
import { CONTAINERS_URL } from "../utils/dataRoutes";

const PAGE_SIZE = 10;

const AgentDockerList = () => {
  const [offset, setOffset] = useState(0);
  const [allContainers, setAllContainers] = useState(false);
  const [search, setSearch] = useState("");
  const [nbContainers, setNbContainers] = useState(0);

  const containersRef = useRef([]);
  const parameters = useMemo(() => ({
    limit: PAGE_SIZE,
    offset,
    search,
    allContainers
  }), [offset, search, allContainers]);

  const { isLoading, error, data, isFetching } = useHTTPDataFetch(CONTAINERS_URL, parameters, 10000);
  
  useEffect(() => {
    if (data && nbContainers !== data.count) {
      setNbContainers(data.count);
    }
    if (data && data.containers) {
      containersRef.current = data.containers;
    }
  }, [data, nbContainers]);

  const handleOffsetChange = useCallback((newOffset) => {
    setOffset(newOffset);
  }, []);

  const handleAllContainersToggle = useCallback((option) => {
    setAllContainers(option === 1);
    setOffset(0);
  }, []);

  const handleSearchChange = useCallback((e) => {
    setSearch(e.target.value);
    setOffset(0);
  }, []);

  let displayContainers;
  if (isLoading && !isFetching) { // Only show loading if initial load is happening
    displayContainers = <Loading size="xl" />;
  } else if (error || !containersRef.current || containersRef.current.length === 0) {
    displayContainers = <QueryError />;
  } else {
    const containersList = containersRef.current;
    const currentCountContainers = data.currentCount;

    const pages = [];
    if (Math.ceil(currentCountContainers / PAGE_SIZE) > 1) {
      for (let i = 0; i < Math.ceil(currentCountContainers / PAGE_SIZE); i++) {
        pages.push(
          <li
            className={`page-item ${i === offset / PAGE_SIZE ? "active" : ""}`}
            key={i.toString()}
          >
            <a className="page-link" onClick={() => handleOffsetChange(i * PAGE_SIZE)}>
              {i + 1}
            </a>
          </li>,
        );
      }
    }

    const pager = pages.length > 0 && (
      <ul className="pagination">
        <li className="page-item">
          <a
            className="page-link"
            aria-label="Previous"
            onClick={() => {
              if (offset > 0) handleOffsetChange(offset - PAGE_SIZE);
            }}
          >
            <span aria-hidden="true">&laquo;</span>
          </a>
        </li>
        {pages}
        <li>
          <a
            className="page-link"
            aria-label="Next"
            onClick={() => {
              if (offset + PAGE_SIZE < currentCountContainers) handleOffsetChange(offset + PAGE_SIZE);
            }}
          >
            <span aria-hidden="true">&raquo;</span>
          </a>
        </li>
      </ul>
    );

    const renderContainers = containersList.map((container) => {
      let date = null;
      if (container.startedAt === null) {
        date = (
          <span>
            <strong>Started&nbsp;at:</strong>
            &nbsp;Never
          </span>
        );
      } else if (container.state === "running") {
        date = (
          <span>
            <strong>Started&nbsp;at:</strong>
            &nbsp;
            {formatDateTime(container.startedAt)}
          </span>
        );
      } else {
        date = (
          <span>
            <strong>Finished&nbsp;at:</strong>
            &nbsp;
            {formatDateTime(container.finishedAt)}
          </span>
        );
      }
      return <Docker container={container} date={date} key={container.id} />;
    });

    displayContainers = (
      <>
        {pager}
        <div className="list-group" style={{ marginBottom: "0.4rem" }}>
          {renderContainers}
        </div>
        {pager}
      </>
    );
  }

  return (
    <div>
      <div className="row">
        <span className="col-xl-7 align-middle col-lg-3">
          <b>Containers :</b> {nbContainers}
        </span>
        <div className="blee-tool-bar col-xl-5 col-lg-9">
          <span className="blee-tool-bar-item py-3">
            <Toggle
              firstOption="Running containers"
              secondOption="All containers"
              onClick={handleAllContainersToggle}
              type="sm"
            />
          </span>

          <span className="blee-tool-bar-item py-3" style={{ flexShrink: "1" }}>
            <DebounceInput
              type="text"
              placeholder="Search"
              className="form-control"
              onChange={handleSearchChange}
              debounceTimeout={500}
              forceNotifyOnBlur={false}
            />
          </span>
        </div>
      </div>

      {displayContainers}
    </div>
  );
};

export default AgentDockerList;
