import * as d3 from "d3";
import { FC } from "react";

import ProcessesTable from "../UI/ProcessesTable";
import AgentProcessesInfo from "./AgentProcessesInfo";

import { isNullOrUndefined, isEmpty } from "../utils";
import { _formatCpuTime } from "../utils/formater";
import { Process, Topinfo } from "../Data/data.interface";
import { isNil } from "lodash-es";

type AgentProcessesProps = {
  top: Topinfo;
  sizePage: number;
};

const AgentProcesses: FC<AgentProcessesProps> = ({ top, sizePage }) => {
  let processesTable: React.ReactNode = null;

  if (top.Processes && !isEmpty(top.Memory) && top.Memory) {
    const processesTmp: Process[] = top.Processes.map((process) => {
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
      if (!isNil(process.ppid)) {
        const nodeProcessChildrens = childrenProcesses.get(process.ppid) || [];
        nodeProcessChildrens.push(process);
        childrenProcesses.set(process.ppid, nodeProcessChildrens);
      }
    });
    const processesLeaves: Process[] = [];
    const processesNodes: Process[] = [];

    const finalProcesses: Process[] = [];

    processesTmp.map((process) => {
      const siblingsProcesses: Process[] | undefined = childrenProcesses.get(
        process.ppid,
      );

      if (
        isNil(process.ppid) ||
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
      const processesWithSameParents: Process[] =
        childrenProcesses.get(process.ppid) || [];
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
            !isNullOrUndefined(p.mem_percent) &&
            p.mem_percent &&
            !isNaN(p.mem_percent)
              ? p.mem_percent
              : 0,
          )
          .reduce(
            (acc, val) =>
              parseFloat(acc.toString()) + parseFloat(val.toString()),
          );

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

    const getProcessesWithSamePPID = (ppid: number) => {
      const processesWithSamePPID = finalProcesses.filter(
        (p) => ppid === p.ppid,
      );
      if (processesWithSamePPID.length === 1) return undefined;
      else return processesWithSamePPID;
    };

    const processesTableData = finalProcesses.map((p: Process) => {
      return {
        ...p,
        subRows: getProcessesWithSamePPID(p.ppid),
      };
    });

    processesTable = (
      <ProcessesTable
        data={processesTableData}
        sizePage={sizePage}
        renderLoadMoreButton
        classNames="fontSmaller"
      />
    );
  }

  return (
    <div>
      <AgentProcessesInfo top={top} />
      <div className="marginOffset">{processesTable}</div>
    </div>
  );
};

export default AgentProcesses;
