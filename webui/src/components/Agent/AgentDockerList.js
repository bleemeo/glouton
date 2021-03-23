import React, { useState } from "react";

import Docker from "./Docker";
import Loading from "../UI/Loading";
import { formatDateTime } from "../utils/formater";
import { DebounceInput } from "react-debounce-input";
import Toggle from "../UI/Toggle";
import QueryError from "../UI/QueryError";
import { useFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";
import { CONTAINERS_DETAILS } from "../utils/gqlRequests";

const PAGE_SIZE = 10;

const AgentDockerList = () => {
  const [offset, setOffset] = useState(0);
  const [allContainers, setAllContainers] = useState(false);
  const [search, setSearch] = useState("");
  const [nbContainers, setNbContainers] = useState(0);

  const { isLoading, error, containers } = useFetch(
    CONTAINERS_DETAILS,
    {
      offset,
      limit: PAGE_SIZE,
      allContainers,
      search,
    },
    10000
  );
  let displayContainers;
  if (isLoading) {
    displayContainers = <Loading size="xl" />;
  } else if (error || isNullOrUndefined(containers)) {
    displayContainers = <QueryError />;
  } else {
    const containersList = containers.containers;
    const newNbContainers = containers.count;
    const currentCountContainers = containers.currentCount;
    if (nbContainers !== newNbContainers) {
      setNbContainers(newNbContainers);
    }
    const pages = [];
    let pager = null;
    if (Math.ceil(currentCountContainers / PAGE_SIZE) > 1) {
      for (let i = 0; i < Math.ceil(currentCountContainers / PAGE_SIZE); i++) {
        pages.push(
          <li
            className={`page-item ${i === offset / PAGE_SIZE ? "active" : ""}`}
            key={i.toString()}
          >
            <a className="page-link" onClick={() => setOffset(i * PAGE_SIZE)}>
              {i + 1}
            </a>
          </li>
        );
      }
      pager = (
        <ul className="pagination">
          <li className="page-item">
            <a
              className="page-link"
              aria-label="Previous"
              onClick={() => {
                if (offset > 0) setOffset(offset - PAGE_SIZE);
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
                  setOffset(offset + PAGE_SIZE);
              }}
            >
              <span aria-hidden="true">&raquo;</span>
            </a>
          </li>
        </ul>
      );
    }
    let renderContainers = null;
    if (containersList.length === 0) {
      if (search) {
        renderContainers = (
          <div className="d-flex justify-content-center align-items-center">
            <h2>There are no containers matching this search</h2>
          </div>
        );
      } else if (allContainers) {
        renderContainers = (
          <div className="d-flex justify-content-center align-items-center">
            <h2>There are no containers</h2>
          </div>
        );
      } else {
        renderContainers = (
          <div className="d-flex justify-content-center align-items-center">
            <h2>There are no running containers</h2>
          </div>
        );
      }
    } else {
      renderContainers = containersList.map((container) => {
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
    }
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
              onClick={(option) => {
                setAllContainers(option === 1);
                setOffset(0);
              }}
              type="sm"
            />
          </span>

          <span className="blee-tool-bar-item py-3" style={{ flexShrink: "1" }}>
            <DebounceInput
              type="text"
              placeholder="Search"
              className="form-control"
              onChange={(e) => {
                setSearch(e.target.value);
                setOffset(0);
              }}
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
