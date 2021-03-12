import React from "react";
import BootstrapTable from "react-bootstrap-table-next";
import paginationFactory, {
  PaginationProvider,
} from "react-bootstrap-table2-paginator";
import PropTypes from "prop-types";
import cn from "classnames";
import { formatToBytes, percentToString2Digits } from "../utils/formater";
import { isNullOrUndefined } from "../utils";
import "react-bootstrap-table-next/dist/react-bootstrap-table2.min.css";
import FaIcon from "./FaIcon";

const cmdLineCommand = ["#C9B202", "#2ecc71", "#3498db"];

class ProcessesTable extends React.PureComponent {
  static propTypes = {
    data: PropTypes.arrayOf(Object).isRequired,
    sizePage: PropTypes.number.isRequired,
    classNames: PropTypes.string,
    widthLastColumn: PropTypes.number,
    borderless: PropTypes.bool,
    expandRow: PropTypes.object,
    renderLoadMoreButton: PropTypes.bool,
    onSortTable: PropTypes.func,
  };

  state = {
    displayAll: false,
  };

  _handleDisplayAll = ({ page, onSizePerPageChange }) => {
    this.setState(
      (prevState) => ({ displayAll: !prevState.displayAll }),
      () => {
        onSizePerPageChange(
          this.state.displayAll ? this.props.data.length : 20,
          page
        );
      }
    );
  };

  onSort = (field, order) => {
    if (this.props.onSortTable) this.props.onSortTable({ field, order });
  };

  renderSortCarets = (order) => {
    if (!order) {
      return (
        <span>
          {" "}
          <FaIcon icon="fa fa-caret-down" />
          <FaIcon icon="fa fa-caret-up" />
        </span>
      );
    } else if (order === "asc") {
      return (
        <span style={{ color: "black" }}>
          {" "}
          <FaIcon icon="fa fa-caret-up" />
        </span>
      );
    } else if (order === "desc") {
      return (
        <span style={{ color: "black" }}>
          {" "}
          <FaIcon icon="fa fa-caret-down" />
        </span>
      );
    }
    return null;
  };

  render() {
    const { displayAll } = this.state;
    const {
      sizePage,
      data,
      widthLastColumn,
      borderless,
      expandRow,
      classNames,
      renderLoadMoreButton,
    } = this.props;
    const columns = [
      {
        dataField: "pid",
        text: "PID",
        headerTitle: function callback() {
          return "Process ID";
        },
        formatter: (cell, row) => {
          if (row.expandable) return "...";
          else return cell;
        },
        sort: true,
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
        headerStyle: { width: "5rem" },
      },
      {
        dataField: "username",
        text: "User",
        headerTitle: function callback() {
          return "User name";
        },
        sort: true,
        formatter: (cell, row) => {
          return (
            <div className="cellEllipsis" style={{ width: "7rem" }}>
              {cell}
            </div>
          );
        },
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
        headerStyle: { width: "7rem" },
      },
      {
        dataField: "memory_rss",
        text: "RES",
        headerClasses: "text",
        headerTitle: function callback() {
          return "Resident Memory Size";
        },
        sort: true,
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        formatter: (cell, row) => (
          <div>
            {cell && !isNaN(cell) ? formatToBytes(cell * 1000).join(" ") : ""}
          </div>
        ),
        headerStyle: { width: "5rem" },
      },
      {
        dataField: "status",
        text: "Status",
        headerTitle: function callback() {
          return "Process status";
        },
        formatter: (cell, row) =>
          cell === "?" || cell === "idle" || cell === "disk-sleep"
            ? "sleeping"
            : cell,
        sort: true,
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
        headerStyle: { width: "5rem" },
      },
      {
        dataField: "cpu_percent",
        text: "%CPU",
        headerTitle: function callback() {
          return "CPU Usage";
        },
        formatter: (cell, row) => {
          return !isNullOrUndefined(cell) && !isNaN(cell) ? (
            <GraphCell value={cell} />
          ) : null;
        },
        sort: true,
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
        headerStyle: { width: "7rem" },
      },
      {
        dataField: "mem_percent",
        text: "%MEM",
        headerTitle: function callback() {
          return "Memory Usage";
        },
        formatter: (cell, row) => {
          return !isNullOrUndefined(cell) && !isNaN(cell) ? (
            <GraphCell value={cell} />
          ) : null;
        },
        sort: true,
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
        headerStyle: { width: "7rem" },
      },
      {
        dataField: "new_cpu_times",
        text: "TIME+",
        headerTitle: function callback() {
          return "CPU Time, hundredths";
        },
        sort: true,
        sortFunc: (a, b, order, dataField, rowA, rowB) => {
          if (order === "asc") return rowA.cpu_times - rowB.cpu_times;
          else return rowB.cpu_times - rowA.cpu_times;
        },
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
        headerStyle: { width: "5rem" },
      },
      {
        dataField: "cmdline",
        text: "Name",
        headerTitle: function callback() {
          return "Command line";
        },
        formatter: (cell, row) => {
          return formatCmdLine(cell, widthLastColumn, row.expandable);
        },
        sort: true,
        onSort: this.onSort,
        sortCaret: this.renderSortCarets,
        headerClasses: "text",
      },
    ];
    const defaultSorted = [
      {
        dataField: "cpu_percent",
        order: "desc",
      },
    ];

    return (
      <div>
        <PaginationProvider
          pagination={paginationFactory({
            custom: true,
            sizePerPage: displayAll ? data.length : 20,
            totalSize: data.length,
            page: 1,
          })}
        >
          {({ paginationProps, paginationTableProps }) => (
            <div>
              <div className="d-flex justify-content-center text-truncate borderless">
                <BootstrapTable
                  classes={"table " + classNames ? classNames : ""}
                  rowClasses={cn("rowHeightReduced", {
                    borderless: borderless,
                  })}
                  rowStyle={{ color: "#000" }}
                  bordered={false}
                  bootstrap4
                  hover
                  keyField="pid"
                  data={data}
                  columns={columns}
                  defaultSorted={defaultSorted}
                  expandRow={expandRow}
                  {...paginationTableProps}
                />
              </div>
              {renderLoadMoreButton && data.length > sizePage ? (
                <div className="fixed-bottom text-center">
                  <button
                    type="button"
                    className="btn btn-primary"
                    style={{ marginBottom: ".5rem" }}
                    onClick={() => this._handleDisplayAll(paginationProps)}
                  >
                    {displayAll ? "Show less processes" : "Show all processes"}
                  </button>
                </div>
              ) : null}
            </div>
          )}
        </PaginationProvider>
      </div>
    );
  }
}

export default ProcessesTable;

export const PercentBar = ({ color, title, percent }) => (
  <div
    className="percent-bar"
    title={title}
    data-toggle="tooltip"
    style={{ backgroundColor: color, width: percent + "%", height: "100%" }}
  />
);

PercentBar.propTypes = {
  color: PropTypes.string.isRequired,
  title: PropTypes.string,
  percent: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
};

export const GraphCell = ({ value }) => (
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

GraphCell.propTypes = {
  value: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
};

export const formatCmdLine = (input, widthLastColumn, expandable = false) => {
  if (expandable) {
    return (
      <div
        className="cellEllipsis"
        style={{
          maxWidth: widthLastColumn ? widthLastColumn + "rem" : "59rem",
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
      style={{
        maxWidth: widthLastColumn ? widthLastColumn + "rem" : "59rem",
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
