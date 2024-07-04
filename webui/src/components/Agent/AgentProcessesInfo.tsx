import React, { FC } from "react";
import PropTypes from "prop-types";
import * as d3 from "d3";
import { bytesToString, formatDateTimeWithSeconds } from "../utils/formater";
import { chartColorMap } from "../utils/colors";
import { Topinfo } from "../Data/data.interface";

type PercentBarProps = {
  color: string;
  title: string;
  percent: number;
};

const PercentBar: FC<PercentBarProps> = ({ color, title, percent }) => (
  <div
    className="percent-bar"
    title={title}
    data-toggle="tooltip"
    style={{ backgroundColor: color, width: percent + "%" }}
  />
);

const formatUptime = (uptimeSeconds: number) => {
  const uptimeDays = Math.trunc(uptimeSeconds / (24 * 60 * 60));
  const uptimeHours = Math.trunc((uptimeSeconds % (24 * 60 * 60)) / (60 * 60));
  const uptimeMinutes = Math.trunc((uptimeSeconds % (60 * 60)) / 60);

  const textMinutes = uptimeDays > 1 ? "minutes" : "minute";
  const textHours = uptimeHours > 1 ? "hours" : "hour";
  const textDays = uptimeMinutes > 1 ? "days" : "day";

  let uptimeString;
  if (uptimeDays === 0 && uptimeHours === 0) {
    uptimeString = `${uptimeMinutes} ${textMinutes}`;
  } else if (uptimeDays === 0) {
    uptimeString = `${uptimeHours} ${textHours}`;
  } else {
    uptimeString = `${uptimeDays} ${textDays}, ${uptimeHours} ${textHours}`;
  }

  return uptimeString;
};

type AgentProcessesInfoProps = {
  top: Topinfo
};

const AgentProcessesInfo: FC<AgentProcessesInfoProps> = ({ top }) => {

  if (!top || !top.CPU || !top.Memory || !top.Swap || !top.Loads || !top.Processes) {
    return <div>Error: No data available</div>;
  } else {
    
  }

  const timeDate = new Date(top["Time"]);

  // sometimes the sum of all CPU percentage make more than 100%
  // which broke the bar, so we have to re-compute them

  const cpuTotal = 
    top.CPU.System +
    top.CPU.User +
    top.CPU.Nice +
    top.CPU.IOWait +
    top.CPU.Idle;
    
  const cpuSystemPerc = (top.CPU.System / cpuTotal) * 100;
  const cpuUserPerc = (top.CPU.User / cpuTotal) * 100;
  const cpuNicePerc = (top.CPU.Nice / cpuTotal) * 100;
  const cpuWaitPerc = (top.CPU.IOWait / cpuTotal) * 100;
  const cpuIdlePerc = (top.CPU.Idle / cpuTotal) * 100;

  const cpuTooltipMsg =
    `${d3.format(".2r")(cpuSystemPerc)}% system` +
    ` ‒ ${d3.format(".2r")(cpuUserPerc)}% user` +
    ` ‒ ${d3.format(".2r")(cpuNicePerc)}% nice` +
    ` ‒ ${d3.format(".2r")(cpuWaitPerc)}% wait` +
    ` ‒ ${d3.format(".2r")(cpuIdlePerc)}% idle`;

  let memTotal =
    top.Memory.Used +
    top.Memory.Free +
    top.Memory.Buffers +
    top.Memory.Cached;

  let memUsed = top.Memory.Used;
  const memUsedPerc = (memUsed / memTotal) * 100;
  const memFreePerc = (top.Memory.Free / memTotal) * 100;
  const memBuffersPerc = (top.Memory.Buffers / memTotal) * 100;
  const memCachedPerc = (top.Memory.Cached / memTotal) * 100;

  const memTooltipMsg =
    `${bytesToString(memUsed * 1024)} used` +
    ` ‒ ${bytesToString(top.Memory.Buffers * 1024)} buffers` +
    ` ‒ ${bytesToString(top.Memory.Cached * 1024)} cached` +
    ` ‒ ${bytesToString(top.Memory.Free * 1024)} free`;

  const swapUsedPerc = (top.Swap.Used / top.Swap.Total) * 100;
  const swapFreePerc = (top.Swap.Free / top.Swap.Total) * 100;

  const swapTooltipMsg =
    `${bytesToString(top.Swap.Used * 1024)} used` +
    ` ‒ ${bytesToString(top.Swap.Free * 1024)} free`;
  const maxLoad = Math.max(...top.Loads);
  const loadTooltipMdg =
    top.Loads[0] + "\n" + top.Loads[1] + "\n" + top.Loads[2];
  return (
    <div className="row">
      <div className="col-lg-8">
        <table
          className="table table-sm borderless"
          style={{ marginBottom: 0 }}
        >
          <tbody>
            <tr>
              <td className="percent-bar-label">
                <strong>Cpu(s):</strong>
              </td>
              <td>
                <div className="percent-bars">
                  <PercentBar
                    color="#e67e22"
                    percent={cpuSystemPerc}
                    title={cpuTooltipMsg}
                  />
                  <PercentBar
                    color="#467FCF"
                    percent={cpuUserPerc}
                    title={cpuTooltipMsg}
                  />
                  <PercentBar
                    color="#4DD0E1"
                    percent={cpuNicePerc}
                    title={cpuTooltipMsg}
                  />
                  <PercentBar
                    color="#e74c3c"
                    percent={cpuWaitPerc}
                    title={cpuTooltipMsg}
                  />
                  <PercentBar
                    color="#2ecc71"
                    percent={cpuIdlePerc}
                    title={cpuTooltipMsg}
                  />
                </div>
              </td>
            </tr>
            <tr>
              <td className="percent-bar-label">
                <strong>Mem:</strong>
              </td>
              <td>
                <div className="percent-bars">
                  <PercentBar
                    color="#467FCF"
                    percent={memUsedPerc}
                    title={memTooltipMsg}
                  />
                  <PercentBar
                    color="#95a5a6"
                    percent={memBuffersPerc}
                    title={memTooltipMsg}
                  />
                  <PercentBar
                    color="#f1c40f"
                    percent={memCachedPerc}
                    title={memTooltipMsg}
                  />
                  <PercentBar
                    color="#2ecc71"
                    percent={memFreePerc}
                    title={memTooltipMsg}
                  />
                </div>
              </td>
            </tr>
            <tr>
              <td className="percent-bar-label">
                <strong>Swap:</strong>
              </td>
              <td>
                <div className="percent-bars">
                  <PercentBar
                    color="#467FCF"
                    percent={swapUsedPerc}
                    title={swapTooltipMsg}
                  />
                  <PercentBar
                    color="#2ecc71"
                    percent={swapFreePerc}
                    title={swapTooltipMsg}
                  />
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div className="col-lg-3">
        <table
          className="table table-sm borderless"
          style={{ marginBottom: 0 }}
        >
          <tbody>
            <tr>
              <td colSpan={5}>
                <h4 style={{ marginBottom: 0 }}>
                  <strong style={{ fontSize: "medium" }}>Last update: </strong>
                  <span className="badge badge-secondary">
                    {formatDateTimeWithSeconds(timeDate)}
                  </span>
                </h4>
              </td>
            </tr>
            <tr>
              <td colSpan={5}>
                <strong>Users:</strong> {top.Users}
              </td>
            </tr>
            <tr>
              <td colSpan={5}>
                <strong>Uptime:</strong> {formatUptime(top.Uptime)}
              </td>
            </tr>
            <tr>
              <td colSpan={5}>
                <div
                  style={{
                    display: "flex",
                    alignItems: "left",
                    justifyContent: "right",
                    flexDirection: "row",
                  }}
                >
                  <div style={{ width: "30%" }}>
                    <strong>Load average:</strong>
                  </div>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "left",
                      justifyContent: "right",
                      flexDirection: "column",
                      width: "70%",
                    }}
                  >
                    <div className="percent-bars" style={{ height: "8px" }}>
                      <PercentBar
                        color={chartColorMap(0)}
                        percent={(top.Loads[0] / maxLoad) * 100}
                        title={loadTooltipMdg}
                      />
                    </div>
                    <div className="percent-bars" style={{ height: "8px" }}>
                      <PercentBar
                        color={chartColorMap(1)}
                        percent={(top.Loads[1] / maxLoad) * 100}
                        title={loadTooltipMdg}
                      />
                    </div>
                    <div className="percent-bars" style={{ height: "8px" }}>
                      <PercentBar
                        color={chartColorMap(2)}
                        percent={(top.Loads[2] / maxLoad) * 100}
                        title={loadTooltipMdg}
                      />
                    </div>
                  </div>
                </div>
              </td>
            </tr>
            <tr>
              <td colSpan={5}>
                <strong>Tasks:</strong>
              </td>
            </tr>
          </tbody>
        </table>
        <table className="table table-sm" style={{ marginBottom: 0 }}>
          <tbody>
            <tr>
              <td>{top.Processes.length} total</td>
              <td>
                {
                  top.Processes.filter((p) => p.status === "running")
                    .length
                }{" "}
                running
              </td>
              <td>
                {
                  top.Processes.filter(
                    (p) =>
                      p.status=== "sleeping" ||
                      p.status=== "?" ||
                      p.status=== "idle" ||
                      p.status=== "disk-sleep",
                  ).length
                }{" "}
                sleeping
              </td>
              <td>
                {
                  top.Processes.filter((p) => p.status=== "stopped")
                    .length
                }{" "}
                stopped
              </td>
              <td>
                {
                  top.Processes.filter((p) => p.status=== "zombie")
                    .length
                }{" "}
                zombie
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
};

export default AgentProcessesInfo;
