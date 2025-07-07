import { List } from "@chakra-ui/react";
import { isNil } from "lodash-es";
import React, { FC, RefObject } from "react";
import { FaArrowDown, FaArrowUp, FaCheckCircle, FaMoon } from "react-icons/fa";

export const chartTypes = ["gauge", "number", "numbers"];
export const UNIT_FLOAT = 0;
export const UNIT_PERCENTAGE = 1;
export const UNIT_INT = 2;
export const LabelName = "__name__";

export const createFilterFn = (input: string) => {
  const i = input.toLowerCase();
  return (text: string) => text.toLowerCase().includes(i);
};

export const isNullOrUndefined = (variable: unknown) => isNil(variable);

export const isUndefined = (variable) => isNil(variable);

type ProblemsProps = {
  problems: string[];
};
export const Problems: FC<ProblemsProps> = ({ problems }) => (
  <List.Root>
    {problems
      ? problems.map((problem, idx) => (
          <List.Item key={idx}>{problem}</List.Item>
        ))
      : null}
  </List.Root>
);

type MetricDescriptionProps = {
  description: string;
};
export const MetricDescription: FC<MetricDescriptionProps> = ({
  description,
}) => {
  if (isNil(description)) {
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
  if (!isNil(copyElement)) {
    copyElement.appendChild(el);
    el.select();
    document.execCommand("Copy");
    copyElement.removeChild(el);
  }
};

export type Period = {
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

export const computeEnd = (period: Period) => {
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
  const end = computeEnd(period);
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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const useIntersection = (element: RefObject<any>, rootMargin) => {
  const [isVisible, setIsVisible] = React.useState(false);

  React.useEffect(() => {
    const current = element?.current;
    const observer = new IntersectionObserver(
      ([entry]) => {
        setIsVisible(entry.isIntersecting);
      },
      { rootMargin },
    );

    if (current) observer?.observe(current);

    return () => {
      if (current) {
        observer.unobserve(current);
      }
    };
  }, []);

  return isVisible;
};

export const iconFromName = (
  name: string,
  w: number,
  h: number,
  color: string,
) => {
  switch (name) {
    case "arrow-up":
      return <FaArrowUp width={w} height={h} color={color} />;
    case "arrow-down":
      return <FaArrowDown width={w} height={h} color={color} />;
    case "process-sleeping":
      return <FaMoon width={w} height={h} color={color} />;
    case "process-running":
      return <FaCheckCircle width={w} height={h} color={color} />;
    default:
      return <></>;
  }
};
