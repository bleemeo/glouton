import React, { useRef, useEffect, useState } from "react";
import PropTypes from "prop-types";
import { Card } from "tabler-react";
import * as echarts from "echarts";
import cn from "classnames";
import {
  formatToFrenchTime,
  tickFormatDate,
  percentToString,
  bytesToString,
  bitsToString,
  iopsToString,
} from "../utils/formater";
import Loading from "./Loading";
import {
  fillEmptyPoints,
  UNIT_PERCENTAGE,
  composeMetricName,
  LabelName,
} from "../utils";
import FaIcon from "./FaIcon";
import QueryError from "./QueryError";
import { chartColorMap } from "../utils/colors";

const CPU = [
  "#C49C94",
  "#F7B6D2",
  "#C5B0D5",
  "#FF7F0D",
  "#BDD1EC",
  "#9DD9E5",
  "#D62728",
  "#98DF89",
];
const MEMORY = ["#AEC7E8", "#C7C7C7", "#E1E1A2", "#98DF8A"];

export const getOptions = (series, stacked, funcConverter, unit) => ({
  colors: series.map((serie) => serie.color),
  animation: false,
  grid: {
    top: "3%",
    left: "1%",
    right: "1%",
    bottom: "8%",
    containLabel: true,
  },
  xAxis: [
    {
      type: "time",
      boundaryGap: false,
      splitNumber: 10,
      axisLabel: {
        formatter: function (value) {
          // Formatted to be month/day; display year only in the first label
          var date = new Date(value);
          return tickFormatDate(date);
        },
        color: "#000",
      },
      axisTick: {
        lineStyle: {
          color: "#000",
        },
      },
      axisLine: {
        lineStyle: {
          color: "#000",
        },
      },
    },
  ],
  yAxis: [
    {
      type: "value",
      show: true,
      min: 0,
      max: unit === UNIT_PERCENTAGE ? 100 : null,
      axisLabel: {
        formatter: function (value) {
          // Formatted to be month/day; display year only in the first label
          return funcConverter(value);
        },
        color: "#000",
      },
      axisTick: {
        lineStyle: {
          color: "#000",
        },
      },
      axisLine: {
        lineStyle: {
          color: "#000",
        },
      },
    },
  ],
  series: series,
  tooltip: {
    trigger: "axis",
    transitionDuration: 0.2,
    hideDelay: 0,
    backgroundColor: "#ffffff",
    borderWidth: 1,
    textStyle: {
      color: "#000",
    },
    axisPointer: {
      type: "line",
    },
    formatter: function (params) {
      let total = -1;
      if (stacked) {
        total = 0;
        params.map((p) => {
          total += p.data[1];
          return params;
        });
      }
      let html = `<div">${formatToFrenchTime(params[0].data[0])}</div>`;
      html += "<table><tbody>";
      params.map(
        (p) =>
          (html += `<tr>
                <td>
                  <div style="width: 12px; height: 12px; border-radius: 6px; background-color: ${
                    p.color
                  }"/>
                </td>
                <td>
                  ${p.seriesName}
                </td>
                <td>
                  <b>${
                    p.data[1] !== null && p.data[1] !== undefined
                      ? funcConverter(p.data[1])
                      : "N/A"
                  }</b>
                </td>
              </tr>
            `)
      );
      html +=
        total > -1
          ? `
                    <tr>
                      <td/>
                      <td><b>TOTAL</b></td>
                      <td><b>${funcConverter(total)}</b></td>
                    </tr>
                  `
          : "";
      html += "</tbody></table>";
      return html;
    },
  },
});

const selectUnitConverter = (unit) => {
  switch (unit) {
    case 1:
      return function (value) {
        return percentToString(value);
      };
    case 2:
      return function (value) {
        return bytesToString(value);
      };
    case 3:
      return function (value) {
        return bitsToString(value);
      };
    case 4:
      return function (value) {
        return iopsToString(value);
      };
    default:
      return function (value) {
        return value.toFixed(2);
      };
  }
};

export const renderLegend = (series, noPointer = true) => {
  let legend = null;
  if (series.length > 0) {
    legend = (
      <div className="chart-legend">
        {series.map((s, idx) => (
          <span
            key={idx}
            className={cn("legend-label", { "no-pointer": noPointer })}
          >
            <div
              className="legend-pill no-selection"
              style={{
                backgroundColor: s.color,
                borderColor: s.color,
              }}
            />
            {s.seriesName}
          </span>
        ))}
      </div>
    );
  }
  return legend;
};

const LineChart = ({
  stacked,
  metrics,
  title,
  unit,
  loading,
  hasError,
  period,
  handleBackwardForward,
  windowWidth,
}) => {
  const svgChart = useRef(null);
  const [series, setSeries] = useState([]);

  useEffect(() => {
    if (
      svgChart.current &&
      metrics &&
      metrics.length > 0 &&
      metrics[0].points &&
      metrics[0].points.length > 1 &&
      metrics[0].labels
    ) {
      const series = [];
      /* eslint-disable indent */
      const skipMetricName =
        metrics.length > 1
          ? metrics.every(
              (m) =>
                m.labels.find((l) => l.key === LabelName).value ===
                metrics[0].labels.find((l) => l.key === LabelName).value
            )
          : false;
      /* eslint-enable indent */
      metrics.forEach((metric, idx) => {
        const nameDisplay = composeMetricName(metric, skipMetricName);
        let data = metric.points.map((point) => [point.time, point.value]);
        data = fillEmptyPoints(data, period);
        let color = chartColorMap(idx);
        if (title === "Processor Usage") {
          color = CPU[idx];
        } else if (title === "Memory Usage") {
          color = MEMORY[idx];
        }
        series.push({
          id: idx,
          type: "line",
          color: color,
          name: nameDisplay,
          seriesName: nameDisplay,
          data,
          symbol: "none",
          areaStyle: stacked ? { opacity: 0.9 } : null,
          lineStyle: { width: "1" },
          stack: stacked ? "stack" : null,
        });
      });
      const svg = echarts.init(svgChart.current);
      setSeries(series);
      svg.setOption(
        getOptions(series, stacked, selectUnitConverter(unit), unit)
      );
    }
  }, [svgChart.current, metrics]);

  useEffect(() => {
    if (svgChart.current) {
      const svg = echarts.init(svgChart.current);
      svg.resize();
    }
  }, [windowWidth]);

  let doNotDisplayCarets = false;
  let noData = false;
  if (loading) {
    return (
      <Card className="widgetChart widgetLoading">
        <Card.Body className="noPaddingHorizontal">
          <div className="d-flex flex-column" style={{ height: "24rem" }}>
            <div
              style={{ borderBottomWidth: "2px" }}
              className="border-bottom border-secondary"
            >
              <h2 style={{ marginLeft: "2rem" }}>{title}</h2>
            </div>
            <div
              style={{ marginTop: "0.4rem", height: "19rem" }}
              className="d-flex flex-row justify-content-center align-items-center"
            >
              <Loading size="xl" />
            </div>
          </div>
        </Card.Body>
      </Card>
    );
  } else if (hasError) {
    return (
      <Card className="widgetChart widgetError">
        <Card.Body className="noPaddingHorizontal">
          <div className="d-flex flex-column" style={{ height: "24rem" }}>
            <div
              style={{ borderBottomWidth: "2px" }}
              className="border-bottom border-secondary"
            >
              <h2 style={{ marginLeft: "2rem" }}>{title}</h2>
            </div>
            <div
              style={{ marginTop: "0.4rem", height: "19rem" }}
              className="d-flex flex-row justify-content-center align-items-center"
            >
              <QueryError noBorder />
            </div>
          </div>
        </Card.Body>
      </Card>
    );
  } else if (
    metrics &&
    metrics.length > 0 &&
    metrics[0].points &&
    metrics[0].points.length < 2
  ) {
    doNotDisplayCarets = true;
    return (
      <Card className="widgetChart">
        <Card.Body className="noPaddingHorizontal">
          <div className="d-flex flex-column" style={{ height: "24rem" }}>
            <div
              style={{ borderBottomWidth: "2px" }}
              className="border-bottom border-secondary"
            >
              <h2 style={{ marginLeft: "2rem" }}>{title}</h2>
            </div>
            <div
              style={{ marginTop: "0.4rem", height: "19rem" }}
              className="d-flex flex-row justify-content-center align-items-center widget"
            >
              <h2>
                Please wait a moment while we collect more points for this chart
              </h2>
            </div>
          </div>
        </Card.Body>
      </Card>
    );
  } else if (!metrics || metrics.length === 0) {
    doNotDisplayCarets = true;
    return (
      <Card className="widgetChart">
        <Card.Body className="noPaddingHorizontal">
          <div className="d-flex flex-column" style={{ height: "24rem" }}>
            <div
              style={{ borderBottomWidth: "2px" }}
              className="border-bottom border-secondary"
            >
              <h2 style={{ marginLeft: "2rem" }}>{title}</h2>
            </div>
            <div
              style={{ marginTop: "0.4rem", height: "19rem" }}
              className="d-flex flex-row justify-content-center align-items-center widget"
            >
              <h2 style={{ textAlign: "center" }}>
                No metrics available for this chart
                <br />
                Please wait a moment
              </h2>
            </div>
          </div>
        </Card.Body>
      </Card>
    );
  } else if (metrics && metrics.length > 0 && !metrics[0].points) {
    noData = true;
  }

  return (
    <Card className="widgetChart">
      <Card.Body className="noPaddingHorizontal">
        <div className="d-flex flex-column" style={{ height: "24rem" }}>
          <div
            style={{ borderBottomWidth: "2px" }}
            className="border-bottom border-secondary"
          >
            <h2 style={{ marginLeft: "2rem" }}>{title}</h2>
          </div>
          {doNotDisplayCarets ? null : (
            <>
              <span
                style={{
                  position: "absolute",
                  left: "0",
                  top: "50%",
                  transform: "translate(50%, -70%)",
                  cursor: "pointer",
                }}
                onClick={() => handleBackwardForward()}
              >
                <FaIcon icon="fa fa-angle-left fa-5x" />
              </span>
              {/* If there is less than 20 seconds between end date and now, we do not display forward arrow */}
              {period.to &&
              Math.floor(
                (new Date().getTime() - new Date(period.to).getTime()) / 1000
              ) > 60 ? (
                <span
                  style={{
                    position: "absolute",
                    right: "0",
                    top: "50%",
                    transform: "translate(-60%, -70%)",
                    cursor: "pointer",
                  }}
                  onClick={() => handleBackwardForward(true)}
                >
                  <FaIcon icon="fa fa-angle-right fa-5x" />
                </span>
              ) : null}
            </>
          )}
          <div
            className="widget"
            style={{ marginTop: "0.4rem", height: "17rem" }}
          >
            {renderLegend(series)}
            {noData ? (
              <div
                style={{ height: "22rem", marginTop: "-2rem" }}
                className="d-flex flex-row justify-content-center align-items-center"
              >
                <h2>No data available on this period</h2>
              </div>
            ) : null}
            <div
              ref={svgChart}
              style={{
                width: svgChart.current
                  ? svgChart.current.parentNode.offsetWidth - 100 + "px"
                  : "92%",
                height: "100%",
                marginLeft: "2.5rem",
                marginTop: "0.4rem",
                marginBottom: "0.4rem",
                marginRight: "2.8rem",
              }}
            />
          </div>
        </div>
      </Card.Body>
    </Card>
  );
};

LineChart.propTypes = {
  stacked: PropTypes.bool,
  metrics: PropTypes.instanceOf(Array),
  title: PropTypes.string.isRequired,
  unit: PropTypes.number,
  loading: PropTypes.bool,
  hasError: PropTypes.object,
  period: PropTypes.object,
  handleBackwardForward: PropTypes.func,
  windowWidth: PropTypes.number,
};

export default LineChart;
