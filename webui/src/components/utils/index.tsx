/* eslint-disable indent */
import React from "react";

export const chartTypes = ["gauge", "stacked", "line"];
export const UNIT_PERCENTAGE = 1;
export const UNIT_BYTE = 2;
export const UNIT_NUMBER = 0;
export const LabelName = "__name__";

export const createFilterFn = (input: string) => {
  const i = input.toLowerCase();
  return (text: string) => text.toLowerCase().includes(i);
};

export const isNullOrUndefined = (variable: unknown) =>
  variable === null || variable === undefined;

export const isUndefined = (variable) => variable === undefined;

type ProblemsProps = {
  problems: string[];
};
export const Problems: React.FC<ProblemsProps> = ({ problems }) => (
  <div>
    <ul className="list-unstyled mb-0">
      {problems
        ? problems.map((problem, idx) => <li key={idx}>{problem}</li>)
        : null}
    </ul>
  </div>
);

type MetricDescriptionProps = {
  description: string;
};
export const MetricDescription: React.FC<MetricDescriptionProps> = ({
  description,
}) => {
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

export const copyToClipboard = (cmd: string) => {
  const el = document.createElement("textarea");
  el.value = cmd;
  const copyElement = document.getElementById("copy");
  if (copyElement !== null) {
    copyElement.appendChild(el);
    el.select();
    document.execCommand("Copy");
    copyElement.removeChild(el);
  }
};

export const isDarkTheme = (theme: string) => theme === "dark";

export const getThemeBackgroundColorsSelect = (theme: string) =>
  isDarkTheme(theme)
    ? ["#045FB4", "#0B3861", "#3a3a3a"]
    : ["#A9D0F5", "#EFF5FB", "#FFF"];

type Period = {
  minutes: number;
  from?: Date;
  to?: Date;
};
export const computeStart = (type: string, period: Period) => {
  if (chartTypes[0] === type) {
    if (period.minutes) {
      return new Date(
        new Date().setMinutes(new Date().getMinutes() - 1),
      ).toISOString();
    } else {
      const end = period.to ? new Date(period.to) : new Date();
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
      return period.from
        ? new Date(period.from).toISOString()
        : new Date().toISOString();
    }
  }
};

export const computeEnd = (type: string, period: Period) => {
  if (period.minutes) {
    return new Date().toISOString();
  } else {
    return period.to
      ? new Date(period.to).toISOString()
      : new Date().toISOString();
  }
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const fillEmptyPoints = (data: any, period: Period) => {
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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const isEmpty = (obj: any) => {
  for (const key in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) return false;
  }
  return true;
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function composeMetricName(metric: any, name: string) {
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
