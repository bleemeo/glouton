import * as d3 from "d3";
import React, { useState } from "react";
import PropTypes from "prop-types";

import FaIcon from "../UI/FaIcon";
import { _formatCpuTime } from "../utils/formater";
import { formatDateTime } from "../utils/formater";
import { renderDisk } from "./utils";
import { renderNetwork, renderDonutDocker } from "../UI";
import ProcessesTable from "../UI/ProcessesTable";
import { useHTTPDataFetch } from "../utils/hooks";
import FetchSuspense from "../UI/FetchSuspense";
import { PROCESSES_URL } from "../utils/dataRoutes";
import Modal from "../UI/Modal";
import A from "../UI/A";

const DockerProcesses = ({ containerId, name }) => {
  const { isLoading, error, data } = useHTTPDataFetch(PROCESSES_URL, { containerId }, 10000);
  const processes = data;

  return (
    <div
      className="d-flex flex-column align-items-center mt-3"
      style={{ minHeight: "10rem" }}
    >
      <h3>Processes</h3>
      <FetchSuspense isLoading={isLoading} error={error} processes={processes}>
        {({ processes }) => {
          const result = processes;
          const dockerProcesses =
            result && result["Processes"] ? result["Processes"] : [];
          if (!dockerProcesses || dockerProcesses.length === 0) {
            return <h4>There are no processes related to {name}</h4>;
          } else {
            let memTotal = result["Memory"]["Total"];
            dockerProcesses.map((process) => {
              return {
                ...process,
                mem_percent: d3.format(".2r")(
                  (process.memory_rss / memTotal) * 100,
                ),
                new_cpu_times: _formatCpuTime(process.cpu_time),
              };
            });
            return (
              <div style={{ overflow: "auto" }}>
                <ProcessesTable
                  data={dockerProcesses}
                  sizePage={10}
                  classNames="dockerTable"
                  widthLastColumn={40}
                />
              </div>
            );
          }
        }}
      </FetchSuspense>
    </div>
  );
};

DockerProcesses.propTypes = {
  containerId: PropTypes.string.isRequired,
  name: PropTypes.string.isRequired,
};


const Docker = ({ container, date }) => {
  const [dockerInspect, setDockerInspect] = useState(null);
  const [showProcesses, setShowProcesses] = useState(false);

  let modal = null;
  if (dockerInspect) {
    modal = (
      <Modal
        title={dockerInspect.name}
        closeAction={() => setDockerInspect(null)}
        closeLabel="Cancel"
        className=" modal-xlg"
      >
        <pre
          style={{
            maxHeight: "76vh",
            overflowY: "auto",
          }}
        >
          {JSON.stringify(JSON.parse(dockerInspect.inspect), null, 2)}
        </pre>
      </Modal>
    );
  }

  return (
    <>
      {modal}
      <div className="dockerItem list-group-item">
        <div
          className={`item-left-border ${
            container.state === "running" ? "success" : ""
          }`}
        >
          <span className="vertical-text">{container.state.toUpperCase()}</span>
        </div>
        <div className="row flex1 align-items-center justify-content-between px-3">
          <div className="col-xl-3 col-md-6">
            <h3 className="overflow-ellipsis" title={container.name}>
              {container.name}
            </h3>
            <small>{container.id.substring(0, 12)}</small>
            <br />
            <small>
              <a onClick={() => setShowProcesses(!showProcesses)}>
                {showProcesses ? (
                  <>
                    <FaIcon icon="fa fa-caret-down" /> Close Processes
                  </>
                ) : (
                  <>
                    <FaIcon icon="fa fa-caret-right" /> Show Processes
                  </>
                )}
              </a>
            </small>
          </div>
          <div className="col-xl-6 pull-xl-3 col-sm-12">
            <div className="blee-row">
              {renderDonutDocker("Memory", container.memUsedPerc)}
              {renderDonutDocker("CPU", container.cpuUsedPerc)}
              {renderNetwork(
                "Network IO",
                container.netBitsRecv,
                container.netBitsSent,
              )}
              {renderDisk(
                "Disk IO",
                container.ioWriteBytes,
                container.ioReadBytes,
              )}
            </div>
          </div>
          <div className="col-xl-3 push-xl-5 col-md-6 blee-row">
            <div style={{ minWidth: 0 }}>
              <small>
                <strong>Created&nbsp;at:</strong>
                &nbsp;
                {formatDateTime(container.createdAt)} {date}
                <br />
                <strong>Image&nbsp;name:</strong>
                &nbsp;
                {container.image}
                <br />
                <div className="overflow-ellipsis">
                  <strong>Cmd:</strong>
                  &nbsp;
                  <span title={container.command}>{container.command}</span>
                </div>
              </small>
            </div>
          </div>
          <div
            style={{
              position: "absolute",
              top: "0.5rem",
              right: "0.5rem",
              width: "unset",
              paddingRight: "unset",
            }}
          >
            <A
              onClick={() =>
                setDockerInspect({
                  name: container.name,
                  inspect: container.inspectJSON,
                })
              }
            >
              <FaIcon icon="fa fa-search-plus fa-2x" />
            </A>
          </div>
        </div>
        <div>
          {showProcesses ? (
            <DockerProcesses containerId={container.id} name={container.name} />
          ) : null}
        </div>
      </div>
    </>
  );
};

Docker.propTypes = {
  container: PropTypes.shape({
    id: PropTypes.string.isRequired,
    state: PropTypes.string.isRequired,
    name: PropTypes.string.isRequired,
    command: PropTypes.string.isRequired,
    inspectJSON: PropTypes.string.isRequired,
    image: PropTypes.string.isRequired,
    createdAt: PropTypes.string.isRequired,
    memUsedPerc: PropTypes.number.isRequired,
    cpuUsedPerc: PropTypes.number.isRequired,
    netBitsRecv: PropTypes.number.isRequired,
    netBitsSent: PropTypes.number.isRequired,
    ioReadBytes: PropTypes.number.isRequired,
    ioWriteBytes: PropTypes.number.isRequired,
  }).isRequired,
  date: PropTypes.element.isRequired,
};

export default Docker;