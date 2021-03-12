/* eslint-disable indent */
import React from "react";
import ReactTooltip from "react-tooltip";
import PropTypes from "prop-types";

import { cssClassForStatus, textForStatus } from "./converter";
import { formatDateTime } from "./formater";

export const createFilterFn = (input) => {
  const i = input.toLowerCase();
  return (text) => text.toLowerCase().includes(i);
};

export const chartTypes = ["gauge", "stacked", "line"];
export const UNIT_PERCENTAGE = 1;
export const UNIT_BYTE = 2;
export const UNIT_NUMBER = 0;

export const LabelName = "__name__";

export const isNullOrUndefined = (variable) =>
  variable === null || variable === undefined;

export const isUndefined = (variable) => variable === undefined;

export const Problems = ({ problems }) => (
  <div>
    <ul className="list-unstyled mb-0">
      {problems
        ? problems.map((problem, idx) => <li key={idx}>{problem}</li>)
        : null}
    </ul>
  </div>
);

Problems.propTypes = {
  problems: PropTypes.instanceOf(Array).isRequired,
};

export const MetricDescription = ({ description }) => {
  if (description === undefined) {
    return <noscript />;
  }
  const descriptionPieces = description.split("\n");
  return (
    <div>
      {descriptionPieces.map((descr, idx) => (
        <span key={idx}>
          {descr}
          {idx < descriptionPieces.length - 1 ? (
            <span>&nbsp;&nbsp;&nbsp;</span>
          ) : null}
        </span>
      ))}
    </div>
  );
};

MetricDescription.propTypes = {
  description: PropTypes.string.isRequired,
};

export const tooltipColor = (status) => {
  switch (status) {
    case 0:
      return "success";
    case 1:
      return "warning";
    case 2:
      return "error";
    default:
      return "info";
  }
};

const tooltipContent = (metric) => {
  return (
    <div>
      {typeof metric.current_status_changed_at === "string" ? (
        <span>Since {formatDateTime(metric.current_status_changed_at)}</span>
      ) : (
        <noscript />
      )}
      <MetricDescription description={metric.status_descriptions[0]} />
    </div>
  );
};

export const MetricStatusLabel = ({ metric }) => {
  const status = metric ? metric.current_status : null;
  if (!metric || status === null) {
    return <noscript />;
  }
  return (
    <span
      data-tip
      data-for={`metricStatusLabel${metric.id}`}
      className={`badge badge-${cssClassForStatus(status)}`}
    >
      {textForStatus(status)}
      {typeof metric.current_status_changed_at === "string" ||
      (metric.status_descriptions && metric.status_descriptions.length > 0) ? (
        <ReactTooltip
          id={`metricStatusLabel${metric.id}`}
          place="bottom"
          type={tooltipColor(status)}
          effect="solid"
        >
          <div style={{ marginBottom: "0rem" }}>{tooltipContent(metric)}</div>
        </ReactTooltip>
      ) : (
        <noscript />
      )}
    </span>
  );
};

MetricStatusLabel.propTypes = {
  metric: PropTypes.object,
};

export const copyToClipboard = (cmd) => {
  const el = document.createElement("textarea");
  el.value = cmd;
  document.getElementById("copy").appendChild(el);
  el.select();
  document.execCommand("Copy");
  document.getElementById("copy").removeChild(el);
};

export const isDarkTheme = (theme) => theme === "dark";

export const getThemeBackgroundColorsSelect = (theme) =>
  isDarkTheme(theme)
    ? ["#045FB4", "#0B3861", "#3a3a3a"]
    : ["#A9D0F5", "#EFF5FB", "#FFF"];

export const computeStart = (type, period) => {
  if (chartTypes[0] === type) {
    if (period.minutes) {
      return new Date(
        new Date().setMinutes(new Date().getMinutes() - 1)
      ).toISOString();
    } else {
      const end = new Date(period.to);
      return new Date(
        new Date().setMinutes(end.getMinutes() - 1)
      ).toISOString();
    }
  } else {
    if (period.minutes) {
      return new Date(
        new Date().setMinutes(new Date().getMinutes() - period.minutes)
      ).toISOString();
    } else {
      return new Date(period.from).toISOString();
    }
  }
};

export const computeEnd = (type, period) => {
  if (period.minutes) {
    return new Date().toISOString();
  } else {
    return new Date(period.to).toISOString();
  }
};

export const fillEmptyPoints = (data, period) => {
  const resampling = period.minutes ? period.minutes : 10080;
  const start = computeStart(chartTypes[1], period);
  const end = computeEnd(chartTypes[1], period);
  const firstData = data[0];
  const lastData = data[data.length - 1];
  if (new Date(firstData[0]) > new Date(start)) {
    for (
      let iDate = new Date(
        new Date(firstData[0]).setSeconds(
          new Date(firstData[0]).getSeconds() - resampling / 6
        )
      );
      iDate > new Date(start);
      iDate.setSeconds(iDate.getSeconds() - resampling / 6)
    ) {
      data.splice(0, 0, [
        new Date(iDate.setMilliseconds(0)).toISOString(),
        null,
      ]);
    }
    data.splice(0, 0, [new Date(start).toISOString(), null]);
  }
  if (new Date(lastData[0])) {
    for (
      let iDate = new Date(lastData[0]);
      iDate < new Date(end);
      iDate.setSeconds(iDate.getSeconds() + resampling / 6)
    ) {
      if (new Date(iDate) > new Date(lastData[0])) {
        data.push([new Date(iDate.setMilliseconds(0)).toISOString(), null]);
      }
    }
  }
  return data.filter(
    (d) => new Date(d[0]) >= new Date(start) && new Date(d[0]) <= new Date(end)
  );
};

export const computeBackwardForward = (
  nbMinutes: number,
  startDate: Date,
  endDate: Date,
  isForward: boolean = false
): Object => {
  const applyBackwardOrForward = isForward
    ? nbMinutes * 0.9
    : 0 - nbMinutes * 0.9;
  startDate.setUTCMinutes(startDate.getUTCMinutes() + applyBackwardOrForward);
  endDate.setUTCMinutes(endDate.getUTCMinutes() + applyBackwardOrForward);
  return { startDate, endDate };
};

export const isEmpty = (obj) => {
  for (const key in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) return false;
  }
  return true;
};

export function composeMetricName(metric, skipMetricName = false) {
  let keys = metric.labels.map((l) => l.key).sort();

  if (skipMetricName) {
    keys = keys.filter((l) => l !== LabelName);
  }

  const labelsMap = metric.labels.reduce((acc, l) => {
    acc[l.key] = l.value;
    return acc;
  }, {});
  const nameDisplay = keys.map((key) => labelsMap[key]).join(" ");
  return nameDisplay;
}

export function isShallowEqual(v, o) {
  for (const key in v) {
    if (!(key in o) || v[key] !== o[key]) {
      return false;
    }
  }

  for (const key in o) {
    if (!(key in v) || v[key] !== o[key]) {
      return false;
    }
  }

  return true;
}
