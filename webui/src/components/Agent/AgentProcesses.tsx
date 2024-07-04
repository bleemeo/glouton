import * as d3 from "d3";
import React, { FC, useState } from "react";
import PropTypes from "prop-types";

import { createFilterFn, isNullOrUndefined, isEmpty } from "../utils";
import { _formatCpuTime, formatToBytes } from "../utils/formater";
import ProcessesTable, { formatCmdLine, GraphCell } from "../UI/ProcessesTable";
import AgentProcessesInfo from "./AgentProcessesInfo";
import FaIcon from "../UI/FaIcon";
import { Process, Topinfo } from "../Data/data.interface";

type PercentBarProps = {
  color: string;
  title: string;
  percent: number;
};

const PercentBar = ({ color, title, percent }: PercentBarProps) => (
  <div
    className="percent-bar"
    title={title}
    data-toggle="tooltip"
    style={{ backgroundColor: color, width: percent + "%" }}
  />
);


type AgentProcessesProps = {
  top: Topinfo
  sizePage: number;
};

const AgentProcesses: FC<AgentProcessesProps> = ({ top, sizePage }) => {

  const [filter, setFilter] = useState<string>("");
  const [field, setField] = useState<string>("cpu_percent");
  const [order, setOrder] = useState<string>("asc");
  const [usernamesFilter, setUsernamesFilter] = useState<string[]>([]);

  const getFilteredProcesses = () => {
    const filterFn = createFilterFn(filter);
    return top.Processes.filter((proc) => {
      return (
        (usernamesFilter.length === 0
          ? true
          : usernamesFilter.includes(proc.username)) &&
        (filterFn(proc.pid.toString()) ||
          filterFn(proc.ppid ? proc.ppid.toString() : "") ||
          filterFn(proc.username) ||
          filterFn(proc.cmdline) ||
          filterFn(proc.name))
      );
    });
  };

  const showRowDetail = (row: { ppid: number; }) => {
    const filteredProcesses = getFilteredProcesses();
    const processesWithSamePPID = filteredProcesses.filter(
      (p) => row.ppid === p.ppid,
    );
    const processParent: Process | undefined = top.Processes.find((p) => row.ppid === p.pid);

    processesWithSamePPID.sort((a, b) => {
      if (typeof a[field] === "string" && typeof b[field] === "string") {
        return order === "asc"
          ? a[field].localeCompare(b[field])
          : b[field].localeCompare(a[field]);
      } else {
        return order === "asc" ? a[field] - b[field] : b[field] - a[field];
      }
    });
    if (processesWithSamePPID.length === 1) return null;
    return (
      <div style={{ backgroundColor: "#f2f2f2" }}>
        {processParent ? (
          <table
            style={{ width: "100%", marginLeft: "-0.2rem" }}
            className="borderless"
          >
            <tbody>
              <tr>
                <td style={{ width: "6.4rem" }}>{processParent.pid}</td>
                <td style={{ width: "7rem" }}>{processParent.username}</td>
                <td style={{ width: "5rem" }}>
                  {!isNullOrUndefined(processParent.memory_rss)
                    ? formatToBytes(processParent.memory_rss * 1024)?.join(" ")
                    : ""}
                </td>
                <td style={{ width: "5rem" }}>{processParent.status}</td>
                <td style={{ width: "7rem" }}>
                  {!isNullOrUndefined(processParent.cpu_percent) &&
                    !isNaN(processParent.cpu_percent) ? (
                    <GraphCell value={processParent.cpu_percent} />
                  ) : null}
                </td>
                <td style={{ width: "7rem" }}>
                  {!isNullOrUndefined(processParent.mem_percent) &&
                    processParent.mem_percent &&
                    !isNaN(processParent.mem_percent) ? (
                    <GraphCell value={processParent.mem_percent} />
                  ) : null}
                </td>
                <td style={{ width: "4.5rem" }}>
                  {processParent.new_cpu_times}
                </td>
                <td className="cellEllipsis">
                  {formatCmdLine(processParent.cmdline, null)}
                </td>
              </tr>
            </tbody>
          </table>
        ) : null}
        <table
          style={{ width: "100%", marginLeft: "-0.2rem" }}
          className="borderless"
        >
          <tbody>
            {processesWithSamePPID.map((process) => (
              <tr key={process.pid}>
                <td style={{ width: "2rem" }}>
                  <FaIcon icon="fa fa-level-up-alt fa-rotate-90" />
                </td>
                <td style={{ width: "5rem" }}>{process.pid}</td>
                <td style={{ width: "7rem" }}>{process.username}</td>
                <td style={{ width: "5rem" }}>
                  {!isNullOrUndefined(process.memory_rss)
                    ? formatToBytes(process.memory_rss * 1024)?.join(" ")
                    : ""}
                </td>
                <td style={{ width: "5rem" }}>{process.status}</td>
                <td style={{ width: "7rem" }}>
                  {!isNullOrUndefined(process.cpu_percent) &&
                    !isNaN(process.cpu_percent) ? (
                    <GraphCell value={process.cpu_percent} />
                  ) : null}
                </td>
                <td style={{ width: "7rem" }}>
                  {!isNullOrUndefined(process.mem_percent) &&
                    process.mem_percent &&
                    !isNaN(process.mem_percent) ? (
                    <GraphCell value={process.mem_percent} />
                  ) : null}
                </td>
                <td style={{ width: "5rem" }}>{process.new_cpu_times}</td>
                <td
                  className="cellEllipsis"
                  style={{ color: "#000", width: "42vw" }}
                >
                  {formatCmdLine(process.cmdline, null)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  };

  const showExpandIndicator = ({ expanded, expandable }) => {
    if (!expandable) return null;
    else if (expanded) {
      return (
        <b>
          <FaIcon icon="fa fa-caret-down" />
        </b>
      );
    }
    return (
      <b>
        <FaIcon icon="fa fa-caret-right" />
      </b>
    );
  };


  let processesTable: React.ReactNode = null;

  if (top.Processes && !isEmpty(top.Memory) && top.Memory) {
    const filteredProcesses: Process[] = getFilteredProcesses();

    const processesTmp: Process[] = filteredProcesses.map((process) => {
      return {
        ...process,
        mem_percent: parseFloat(
          d3.format(".2r")((process.memory_rss / top.Memory!.Total) * 100),
        ),
        new_cpu_times: _formatCpuTime(process.cpu_time),
      };
    });

    const childrenProcesses: Map<number, Process[]> = new Map();

    processesTmp.map((process) => {
      if (process.ppid !== undefined) {
        const nodeProcessChildrens =
          childrenProcesses.get(process.ppid) || [];
        nodeProcessChildrens.push(process);
        childrenProcesses.set(process.ppid, nodeProcessChildrens);
      }
    });
    const processesLeaves: Process[] = [];
    const processesNodes: Process[] = [];

    const finalProcesses: Process[] = [];

    processesTmp.map((process) => {
      const siblingsProcesses: Process[] | undefined = childrenProcesses.get(process.ppid);

      if (
        process.ppid === undefined ||
        process.ppid === 1 ||
        !top["Processes"].find((p) => process.ppid === p.pid)
      ) {
        processesNodes.push(process);
      } else if (
        siblingsProcesses &&
        siblingsProcesses.length > 1 &&
        !siblingsProcesses.some(
          (pBrother) =>
            childrenProcesses.get(pBrother.pid) &&
            childrenProcesses.get(pBrother.pid)?.length,
        )
      ) {
        processesLeaves.push(process);
      } else {
        processesNodes.push(process);
      }
    });

    const previousProcesses: number[] = [];

    processesLeaves.map((process) => {
      const processesWithSameParents: Process[] = childrenProcesses.get(process.ppid) || [];
      const processParent: Process | undefined = top.Processes.find(
        (p) => process.ppid === p.pid,
      );

      if (processParent && !previousProcesses.includes(processParent.pid)) {
        previousProcesses.push(processParent.pid);

        const totalRes = [...processesWithSameParents, processParent]
          .map((p) => (!isNullOrUndefined(p.memory_rss) ? p.memory_rss : 0))
          .reduce((acc, val) => acc + val);

        const totalCpu = processesWithSameParents
          .concat(processParent ? [processParent] : [])
          .map((p) =>
            !isNullOrUndefined(p.cpu_percent) && !isNaN(p.cpu_percent)
              ? p.cpu_percent
              : 0,
          )
          .reduce((acc, val) => acc + val);

        const totalMem = [...processesWithSameParents, processParent]
          .map((p) =>
            !isNullOrUndefined(p.mem_percent) && p.mem_percent && !isNaN(p.mem_percent)
              ? p.mem_percent
              : 0,
          )
          .reduce((acc, val) => parseFloat(acc.toString()) + parseFloat(val.toString()));

        finalProcesses.push({
          ...process,
          username: [...processesWithSameParents, processParent].every(
            (p) => p.username === process.username,
          )
            ? process.username
            : "...",
          memory_rss: totalRes,
          cpu_percent: totalCpu,
          mem_percent: totalMem,
          status: [...processesWithSameParents, processParent].every(
            (p) => p.status === process.status,
          )
            ? process.status
            : "...",
          new_cpu_times: _formatCpuTime(
            [...processesWithSameParents, processParent]
              .map((p) => (!isNullOrUndefined(p.cpu_time) ? p.cpu_time : 0))
              .reduce((acc, v) => acc + v),
          ),
          cpu_time: processesWithSameParents
            .map((p) => (!isNullOrUndefined(p.cpu_time) ? p.cpu_time : 0))
            .reduce((acc, val) => acc + val),
          cmdline: processParent.name,
          pid: processParent.pid,
          expandable: true,
        });
      }
    });

    processesNodes
      .filter((process) => !previousProcesses.includes(process.pid))
      .map((process) => {
        finalProcesses.push({
          ...process,
          cmdline: process.cmdline,
          expandable: false,
        });
      });

    const expandRow = {
      renderer: showRowDetail,
      showExpandColumn: true,
      expandHeaderColumnRenderer: ({ isAnyExpands }) => {
        if (isAnyExpands) {
          return <FaIcon icon="fa fa-caret-down" />;
        }
        return (
          <b>
            <FaIcon icon="fa fa-caret-right" />
          </b>
        );
      },
      expandColumnRenderer: showExpandIndicator,
      nonExpandable: processesNodes
        .filter((process) => !previousProcesses.includes(process.pid))
        .map((p) => p.pid),
    };
    processesTable = (
      <ProcessesTable
        data={finalProcesses}
        sizePage={sizePage}
        expandRow={expandRow}
        borderless
        onSortTable={(field, order) => { setField(field); setOrder(order); }}
        renderLoadMoreButton
        classNames="fontSmaller"
      />
    );
  }

  return (
    <div>
      <AgentProcessesInfo top={top} />
      <div className="marginOffset">
        {processesTable}
      </div>
    </div>
  );
}

export default AgentProcesses;