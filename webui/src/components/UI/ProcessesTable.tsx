/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { FC, useMemo, useState } from "react";
import {
  ColumnDef,
  createColumnHelper,
  ExpandedState,
  flexRender,
  getCoreRowModel,
  getExpandedRowModel,
  getFilteredRowModel,
  getSortedRowModel,
  SortingState,
  useReactTable,
} from "@tanstack/react-table";
import { Table as BTable } from "react-bootstrap";

import { Process } from "../Data/data.interface";
import { formatToBytes, percentToString2Digits } from "../utils/formater";

const cmdLineCommand = ["#C9B202", "#2ecc71", "#3498db"];

type PercentBarProps = {
  color: string;
  title?: string;
  percent: string | number;
};

export const PercentBar: FC<PercentBarProps> = ({ color, title, percent }) => (
  <div
    className="percent-bar"
    title={title}
    data-toggle="tooltip"
    style={{ backgroundColor: color, width: percent + "%", height: "100%" }}
  />
);

type GraphCellProps = {
  value: number;
};

export const GraphCell: FC<GraphCellProps> = ({ value }) => (
  <div
    style={{
      display: "flex",
      alignItems: "center",
      justifyContent: "right",
      flexDirection: "row",
    }}
  >
    <div className="d-inline" style={{ width: "50%" }}>
      {percentToString2Digits(value)}
      &nbsp;
    </div>
    <div
      className="percent-bars d-inline graphcellBg littleBorderRadius"
      style={{
        height: "10px",
        width: "50%",
      }}
    >
      <PercentBar color="#3498db" percent={value > 100 ? 100 : value} />
    </div>
  </div>
);

type FormatCmdLineProps = {
  input: string;
  widthLastColumn?: number;
  expandable?: boolean;
};

export const formatCmdLine: FC<FormatCmdLineProps> = ({
  input,
  widthLastColumn,
  expandable,
}) => {
  if (expandable) {
    return (
      <div
        style={{
          maxWidth: widthLastColumn ? widthLastColumn + "rem" : "10rem",
          width: "auto",
          color: cmdLineCommand[1],
        }}
      >
        {input}
      </div>
    );
  }
  const command = input.split(" ")[0];
  const args = input.split(" ").slice(1);
  const regexpEqual = /^-[^=]+=/i;
  const regexpTwoDots = /^-[^\:]+\:/i; // eslint-disable-line no-useless-escape
  const regexpOption = /^-+/i;

  return (
    <div
      className="cellEllipsis"
      id="cmdlineDiv"
      style={{
        maxWidth: widthLastColumn ? widthLastColumn + "rem" : "10rem",
        width: "auto",
      }}
    >
      <span style={{ color: cmdLineCommand[1] }}>{command}</span>
      {args.map((arg, idx) => {
        if (regexpEqual.test(arg)) {
          const splittedArg = arg.split("=", 2);
          return (
            <span
              style={{
                color: cmdLineCommand[0],
              }}
              key={idx.toString()}
            >
              {" "}
              {splittedArg[0]}=
              <span style={{ color: cmdLineCommand[2] }}>{splittedArg[1]}</span>
            </span>
          );
        } else if (regexpTwoDots.test(arg)) {
          const splittedArg = arg.split(":", 2);
          return (
            <span
              style={{
                color: cmdLineCommand[0],
              }}
              key={idx.toString()}
            >
              {" "}
              {splittedArg[0]}:
              <span style={{ color: cmdLineCommand[2] }}>{splittedArg[1]}</span>
            </span>
          );
        } else if (regexpOption.test(arg)) {
          return (
            <span
              style={{
                color: cmdLineCommand[0],
              }}
              key={idx.toString()}
            >
              {" "}
              {arg}
            </span>
          );
        } else {
          return <span key={idx.toString()}> {arg}</span>;
        }
      })}
    </div>
  );
};

interface ProcessTableData extends Process {
  subRows?: Process[];
}

type ProcessesTableProps = {
  data: Process[];
  sizePage: number;
  classNames: string;
  widthLastColumn?: number;
  renderLoadMoreButton?: boolean;
};

const ProcessesTable: FC<ProcessesTableProps> = ({ data, widthLastColumn }) => {
  const [sorting, setSorting] = useState<SortingState>([]);
  const [expanded, setExpanded] = useState<ExpandedState>({});

  const getProcessesWithSamePPID = (ppid: number) => {
    const processesWithSamePPID = data.filter((p) => ppid === p.ppid);

    if (sorting[0] !== undefined) {
      processesWithSamePPID.sort((a, b) => {
        if (
          typeof a[sorting[0].id] === "string" &&
          typeof b[sorting[0].id] === "string"
        ) {
          return !sorting[0].desc
            ? a[sorting[0].id].localeCompare(b[sorting[0].id])
            : b[sorting[0].id].localeCompare(a[sorting[0].id]);
        } else {
          return !sorting[0].desc
            ? a[sorting[0].id] - b[sorting[0].id]
            : b[sorting[0].id] - a[sorting[0].id];
        }
      });
    }
    if (processesWithSamePPID.length === 1) return undefined;
    else return processesWithSamePPID;
  };

  const processes = data.map((p) => {
    return {
      ...p,
      subRows: getProcessesWithSamePPID(p.ppid),
    };
  });

  const columnHelper = createColumnHelper<ProcessTableData>();

  const columns = useMemo<ColumnDef<ProcessTableData>[]>(
    () => [
      columnHelper.accessor("pid", {
        id: "pid",
        header: "PID",
        cell: ({ row, getValue }) => {
          return (
            <div
              style={{
                paddingLeft: `${row.depth * 3}rem`,
              }}
            >
              <div>
                {row.getCanExpand() ? (
                  <a
                    {...{
                      onClick: row.getToggleExpandedHandler(),
                      style: { cursor: "pointer" },
                    }}
                  >
                    {row.getIsExpanded() ? "▼" : "▶ "}
                  </a>
                ) : (
                  ""
                )}{" "}
                {row.depth > 0 ? "▷ " : ""}
                {row.getCanExpand() ? "..." : getValue()}
              </div>
            </div>
          );
        },
      }),
      columnHelper.accessor("username", {
        id: "username",
        header: "User",
        cell: (info) => {
          return (
            <div
              className="cellEllipsis"
              style={{ width: "auto", maxWidth: "5rem" }}
            >
              {info.getValue()}
            </div>
          );
        },
      }),
      columnHelper.accessor("memory_rss", {
        id: "memory_rss",
        header: "RES",
        cell: (info) =>
          formatToBytes((info.getValue() as number) * 1000)?.join(" "),
      }),
      columnHelper.accessor("status", {
        id: "status",
        header: "Status",
        cell: (info) =>
          info.getValue() === "?" ||
          info.getValue() === "idle" ||
          info.getValue() === "disk-sleep"
            ? "sleeping"
            : info.getValue(),
      }),
      columnHelper.accessor("cpu_percent", {
        id: "cpu_percent",
        header: "%CPU",
        cell: (info) => <GraphCell value={info.getValue() as number} />,
      }),
      columnHelper.accessor("mem_percent", {
        id: "mem_percent",
        header: "%MEM",
        cell: (info) => <GraphCell value={info.getValue() as number} />,
      }),
      columnHelper.accessor("new_cpu_times", {
        id: "new_cpu_times",
        header: "TIME+",
        cell: (info) => info.getValue(),
      }),
      columnHelper.accessor("cmdline", {
        id: "cmdline",
        header: "Name",
        cell: (info) =>
          formatCmdLine({
            input: info.getValue() as string,
            widthLastColumn: widthLastColumn,
          }),
      }),
    ],
    [],
  );

  const table = useReactTable({
    data: processes,
    columns: columns,
    getCoreRowModel: getCoreRowModel(),
    defaultColumn: {
      minSize: 0,
      size: 0,
    },
    state: {
      expanded,
      sorting,
    },
    initialState: {
      sorting: [
        {
          id: "cpu_percent",
          desc: true,
        },
      ],
    },
    getSubRows: (row) => row.subRows,
    onExpandedChange: setExpanded,
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    onSortingChange: setSorting,
  });

  return (
    <div className="p-2">
      <BTable striped bordered hover responsive size="sm">
        <thead>
          {table.getHeaderGroups().map((headerGroup) => (
            <tr key={headerGroup.id}>
              {headerGroup.headers.map((header) => {
                return (
                  <th key={header.id} colSpan={header.colSpan}>
                    <div
                      {...{
                        className: header.column.getCanSort()
                          ? "cursor-pointer select-none"
                          : "",
                        onClick: header.column.getToggleSortingHandler(),
                      }}
                    >
                      {flexRender(
                        header.column.columnDef.header,
                        header.getContext(),
                      )}
                      {{
                        asc: " ▲",
                        desc: " ▼",
                      }[header.column.getIsSorted() as string] ?? null}
                    </div>
                  </th>
                );
              })}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr key={row.id}>
              {row.getVisibleCells().map((cell) => (
                <td key={cell.id}>
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
        <tfoot>
          {table.getFooterGroups().map((footerGroup) => (
            <tr key={footerGroup.id}>
              {footerGroup.headers.map((header) => (
                <th key={header.id} colSpan={header.colSpan}>
                  {header.isPlaceholder
                    ? null
                    : flexRender(
                        header.column.columnDef.footer,
                        header.getContext(),
                      )}
                </th>
              ))}
            </tr>
          ))}
        </tfoot>
      </BTable>
    </div>
  );
};

export default ProcessesTable;
