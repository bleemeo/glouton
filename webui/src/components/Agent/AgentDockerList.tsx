import React, { FC, useState, useEffect, useMemo, useCallback } from "react";
import { DebounceInput } from "react-debounce-input";

import Toggle from "../UI/Toggle";
import QueryError from "../UI/QueryError";
import Docker from "./Docker";
import { Loading } from "../UI/Loading";
import { useHTTPDataFetch } from "../utils/hooks";
import { CONTAINERS_URL } from "../utils/dataRoutes";
import { Containers } from "../Data/data.interface";
import { isNil } from "lodash-es";

const PAGE_SIZE = 10;

const AgentDockerList: FC = () => {
  const [offset, setOffset] = useState<number>(0);
  const [allContainers, setAllContainers] = useState<boolean>(false);
  const [search, setSearch] = useState<string>("");
  const [nbContainers, setNbContainers] = useState<number>(0);

  const parameters = useMemo(
    () => ({
      limit: PAGE_SIZE,
      offset,
      search,
      allContainers,
    }),
    [offset, search, allContainers],
  );

  const {
    isLoading,
    error,
    data: containers,
    isFetching,
  } = useHTTPDataFetch<Containers>(CONTAINERS_URL, parameters, 10000);

  useEffect(() => {
    if (containers && nbContainers !== containers.count) {
      setNbContainers(containers.count);
    }
  }, [containers, nbContainers]);

  const handleOffsetChange = useCallback(
    (newOffset: React.SetStateAction<number>) => {
      setOffset(newOffset);
    },
    [],
  );

  const handleAllContainersToggle = useCallback((option: number) => {
    setAllContainers(option === 1);
    setOffset(0);
  }, []);

  const handleSearchChange = useCallback(
    (e: { target: { value: React.SetStateAction<string> } }) => {
      setSearch(e.target.value);
      setOffset(0);
    },
    [],
  );

  let displayContainers: React.JSX.Element | null = null;

  if (isLoading && !isFetching) {
    // Only show loading if initial load is happening
    displayContainers = <Loading size="xl" />;
  } else if (error) {
    displayContainers = <QueryError />;
  } else if (containers) {
    const containersList = containers.containers;
    const currentCountContainers = containers.currentCount;

    const pages: React.JSX.Element[] = [];

    if (Math.ceil(currentCountContainers / PAGE_SIZE) > 1) {
      for (let i = 0; i < Math.ceil(currentCountContainers / PAGE_SIZE); i++) {
        pages.push(
          <li
            className={`page-item ${i === offset / PAGE_SIZE ? "active" : ""}`}
            key={i.toString()}
          >
            <a
              className="page-link"
              onClick={() => handleOffsetChange(i * PAGE_SIZE)}
            >
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
              if (offset + PAGE_SIZE < currentCountContainers)
                handleOffsetChange(offset + PAGE_SIZE);
            }}
          >
            <span aria-hidden="true">&raquo;</span>
          </a>
        </li>
      </ul>
    );

    const renderContainers = containersList.map((container) => {
      let date: [string, string | undefined];
      if (isNil(container.startedAt)) {
        date = ["Started at", "Never"];
      } else if (container.state === "running") {
        date = ["Started at", container.startedAt];
      } else {
        date = ["Finished at", container.finishedAt];
      }
      return (
        <Docker container={container} startedAt={date} key={container.id} />
      );
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
