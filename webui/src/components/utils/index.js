/* eslint-disable indent */
import React from "react";
import PropTypes from "prop-types";

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
        new Date().setMinutes(new Date().getMinutes() - 1),
      ).toISOString();
    } else {
      const end = new Date(period.to);
      return new Date(
        new Date().setMinutes(end.getMinutes() - 1),
      ).toISOString();
    }
  } else {
    if (period.minutes) {
      return new Date(
        new Date().setMinutes(new Date().getMinutes() - period.minutes),
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
          new Date(firstData[0]).getSeconds() - resampling / 6,
        ),
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
  if (new Date(lastData[0]).getTime() !== 0) {
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
    (d) => new Date(d[0]) >= new Date(start) && new Date(d[0]) <= new Date(end),
  );
};

export const isEmpty = (obj) => {
  for (const key in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) return false;
  }
  return true;
};

export function composeMetricName(metric, name) {
  let metricName = name;

  if (metricName.indexOf("{{ device }}") > -1) {
    metricName = metricName.replace("{{ device }}", metric.metric.device);
  } else if (metricName.indexOf("{{ mountpoint }}") > -1) {
    metricName = metricName.replace(
      "{{ mountpoint }}",
      metric.metric.mountpoint,
    );
  } else if (metricName.indexOf("{{ volume }}") > -1) {
    metricName = metricName.replace("{{ volume }}", metric.metric.volume);
  } else if (metricName.indexOf("{{ nic }}") > -1) {
    metricName = metricName.replace("{{ nic }}", metric.metric.nic);
  } else if (metricName.indexOf("{{ item }}") > -1) {
    metricName = metricName.replace("{{ item }}", metric.metric.item);
  }
  return metricName;
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
