import React, { FC, useMemo, useState } from "react";
import {
  ColumnDef,
  createColumnHelper,
  ExpandedState,
  flexRender,
  getCoreRowModel,
  getExpandedRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  PaginationState,
  SortingState,
  useReactTable,
} from "@tanstack/react-table";

import { Process } from "../Data/data.interface";
import { formatToBytes, percentToString2Digits } from "../utils/formater";
import {
  Box,
  Button,
  Center,
  Flex,
  NumberInput,
  Separator,
  Table,
  Text,
} from "@chakra-ui/react";
import { Tooltip } from "./tooltip";

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

interface ProcessesTableData extends Process {
  subRows?: Process[];
}

type ProcessesTableProps = {
  data: ProcessesTableData[];
  sizePage: number;
  classNames: string;
  widthLastColumn?: number;
  renderLoadMoreButton?: boolean;
};

const ProcessesTable: FC<ProcessesTableProps> = ({ data, widthLastColumn }) => {
  const [sorting, setSorting] = useState<SortingState>([
    { id: "cpu_percent", desc: true },
  ]);
  const [expanded, setExpanded] = useState<ExpandedState>({});
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 10,
  });

  const columnHelper = createColumnHelper<ProcessesTableData>();

  const columns = useMemo<ColumnDef<ProcessesTableData>[]>(
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
        cell: (info) => (
          <Tooltip content={info.getValue()}>
            {formatCmdLine({
              input: info.getValue() as string,
              widthLastColumn: widthLastColumn,
            })}
          </Tooltip>
        ),
      }),
    ],
    [],
  );

  const table = useReactTable({
    data: data,
    columns: columns,
    getCoreRowModel: getCoreRowModel(),
    defaultColumn: {
      minSize: 0,
      size: 0,
    },
    state: {
      expanded,
      sorting,
      pagination,
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
    getPaginationRowModel: getPaginationRowModel(),
    onPaginationChange: setPagination,
    onSortingChange: setSorting,
    paginateExpandedRows: false,
  });

  return (
    <Box pl={2} w="100%">
      <Table.Root striped interactive size="sm">
        <Table.Header>
          {table.getHeaderGroups().map((headerGroup) => (
            <Table.Row key={headerGroup.id}>
              {headerGroup.headers.map((header) => {
                return (
                  <Table.ColumnHeader key={header.id} colSpan={header.colSpan}>
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
                  </Table.ColumnHeader>
                );
              })}
            </Table.Row>
          ))}
        </Table.Header>
        <Table.Body>
          {table.getRowModel().rows.map((row) => (
            <Table.Row key={row.id}>
              {row.getVisibleCells().map((cell) => (
                <Table.Cell key={cell.id}>
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </Table.Cell>
              ))}
            </Table.Row>
          ))}
        </Table.Body>
        <Table.Footer>
          {table.getFooterGroups().map((footerGroup) => (
            <Table.Row key={footerGroup.id}>
              {footerGroup.headers.map((header) => (
                <Table.ColumnHeader key={header.id} colSpan={header.colSpan}>
                  {header.isPlaceholder
                    ? null
                    : flexRender(
                        header.column.columnDef.footer,
                        header.getContext(),
                      )}
                </Table.ColumnHeader>
              ))}
            </Table.Row>
          ))}
        </Table.Footer>
      </Table.Root>
      <Flex justify="space-between" align="center">
        <Flex gap={2} alignItems="center">
          <Button
            rounded={"0.25rem"}
            onClick={() => table.firstPage()}
            disabled={!table.getCanPreviousPage()}
          >
            {"<<"}
          </Button>
          <Button
            rounded={"0.25rem"}
            onClick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
          >
            {"<"}
          </Button>
          <Button
            rounded={"0.25rem"}
            onClick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
          >
            {">"}
          </Button>
          <Button
            rounded={"0.25rem"}
            onClick={() => table.lastPage()}
            disabled={!table.getCanNextPage()}
          >
            {">>"}
          </Button>
        </Flex>

        <Flex alignItems="center">
          <Center flexDir="column" alignItems="flex-start">
            <Text mb={0}>Page</Text>
            <Text as="b">
              {table.getState().pagination.pageIndex + 1} of{" "}
              {table.getPageCount().toLocaleString()}
            </Text>
          </Center>
          <Separator orientation="vertical" mx={3} />
          <Center alignItems="center">
            <Text mb={0}>Go to page:</Text>
            <NumberInput.Root
              defaultValue={(
                table.getState().pagination.pageIndex + 1
              ).toString()}
              onChange={(e) => {
                const target = e.target as HTMLInputElement;
                const page = target.value ? Number(target.value) - 1 : 0;
                table.setPageIndex(page);
              }}
              width="200px"
            >
              <NumberInput.Control />
              <NumberInput.Input />
            </NumberInput.Root>
          </Center>
        </Flex>
      </Flex>
    </Box>
  );
};

export default ProcessesTable;
