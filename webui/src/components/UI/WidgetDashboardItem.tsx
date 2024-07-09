/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { FC, useRef } from "react";

import FetchSuspense from "./FetchSuspense";
import MetricGaugeItem from "../Metric/MetricGaugeItem";
import LineChart from "./LineChart";

import { useHTTPPromFetch } from "../utils/hooks";
import { chartTypes } from "../utils";

type WidgetDashboardItemProps = {
  type: string;
  title: string;
  metrics: any;
  unit: number;
  period: any;
  handleBackwardForward?: (isForward: boolean) => void;
  windowWidth?: number;
};

const WidgetDashboardItem: FC<WidgetDashboardItemProps> = ({
  type,
  title,
  metrics,
  unit,
  period,
  handleBackwardForward,
  windowWidth,
}) => {
  const previousError = useRef<any | null>(null);
  const handleBackwardForwardFunc = (isForward = false) => {
    handleBackwardForward ? handleBackwardForward(isForward) : null;
  };

  const displayWidget = (points) => {
    switch (type) {
      case chartTypes[0]: {
        let lastPoint: number = 0;
        if (points[0]) {
          lastPoint = parseFloat(
            points[0].values[points[0].values.length - 1][1],
          );
        }
        const thresholds: {
          highWarning?: number;
          highCritical?: number;
        } = {};
        return (
          <MetricGaugeItem
            unit={unit}
            value={lastPoint}
            thresholds={thresholds}
            name={title}
          />
        );
      }
      case chartTypes[1]: {
        const resultStacked = points;
        return (
          <LineChart
            stacked
            metrics={resultStacked}
            metrics_param={metrics}
            title={title}
            unit={unit}
            period={period}
            handleBackwardForward={handleBackwardForwardFunc}
            windowWidth={windowWidth}
          />
        );
      }
      case chartTypes[2]: {
        const resultsLines = points;
        return (
          <LineChart
            metrics={resultsLines}
            metrics_param={metrics}
            title={title}
            unit={unit}
            period={period}
            handleBackwardForward={handleBackwardForwardFunc}
            windowWidth={windowWidth}
            stacked={false}
          />
        );
      }
    }
  };

  const urls = (metrics, period) => {
    const start = period.from
      ? new Date(period.from).toISOString()
      : new Date(
          new Date().setMinutes(new Date().getMinutes() - period.minutes),
        ).toISOString();
    const end = period.to
      ? new Date(period.to).toISOString()
      : new Date().toISOString();
    const step = 10;
    const urls: string[] = [];

    for (const idx in metrics) {
      urls.push(
        `/api/v1/query_range?query=${encodeURIComponent(
          metrics[idx].query,
        )}&start=${encodeURIComponent(start)}&end=${encodeURIComponent(
          end,
        )}&step=${encodeURIComponent(step)}`,
      );
    }
    return urls;
  };

  const { isLoading, data, error } = useHTTPPromFetch(
    urls(metrics, period),
    10000,
  );
  const points = data;
  let hasError = error;
  if (previousError.current && !error) {
    hasError = previousError.current;
  }
  previousError.current = error;
  return (
    <div>
      {/* See Issue : https://github.com/apollographql/apollo-client/pull/4974 */}
      <FetchSuspense
        isLoading={isLoading || !points || typeof points[0] === "undefined"}
        error={hasError}
        loadingComponent={
          type === chartTypes[0] ? (
            <MetricGaugeItem loading name={title} />
          ) : (
            <LineChart title={title} metrics_param={metrics} loading />
          )
        }
        fallbackComponent={
          type === chartTypes[0] ? (
            <MetricGaugeItem
              hasError={hasError ? hasError : undefined}
              name={title}
            />
          ) : (
            <LineChart
              title={title}
              metrics_param={metrics}
              hasError={hasError}
            />
          ) /* eslint-disable-line react/jsx-indent */
        }
        points={points}
      >
        {({ points }) => displayWidget(points)}
      </FetchSuspense>
    </div>
  );
};

export default WidgetDashboardItem;
