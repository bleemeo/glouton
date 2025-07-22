/* eslint-disable @typescript-eslint/no-explicit-any */
import { FC, useRef } from "react";

import FetchSuspense from "./FetchSuspense";
import MetricGaugeItem from "../Metric/MetricGaugeItem";

import { useHTTPPromFetch } from "../utils/hooks";
import { chartTypes, Period } from "../utils";
import MetricNumberItem from "../Metric/MetricNumberItem";
import MetricNumbersItem from "../Metric/MetricNumbersItem";
import { Metric, MetricFetchResult } from "../Metric/DefaultDashboardMetrics";
import { Box } from "@chakra-ui/react";

type WidgetDashboardItemProps = {
  type: string;
  title: string;
  metrics: Metric[];
  unit: number;
  period: any;
};

const WidgetDashboardItem: FC<WidgetDashboardItemProps> = ({
  type,
  title,
  metrics,
  unit,
  period,
}) => {
  const previousError = useRef<any | null>(null);

  const displayWidget = (points: MetricFetchResult[]) => {
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
        let lastPoint: number = 0;
        if (points[0]) {
          lastPoint = parseFloat(
            points[0].values[points[0].values.length - 1][1],
          );
        }
        return <MetricNumberItem unit={unit} value={lastPoint} title={title} />;
      }
      case chartTypes[2]: {
        const data: {
          value: number;
          legend: string;
          icon?: { name: string; color: string };
        }[] = [];
        metrics.forEach((metric) => {
          const pointsWithMetric = points.filter((point) =>
            point.metric.query.includes(metric.query),
          );
          let lastPoint: number = 0;
          if (pointsWithMetric[0]) {
            lastPoint = parseFloat(
              pointsWithMetric[0].values[
                pointsWithMetric[0].values.length - 1
              ][1],
            );
          }
          data.push({
            value: lastPoint,
            legend: metric.legend ? metric.legend : "",
            icon: metric.icon,
          });
        });
        return <MetricNumbersItem unit={unit} data={data} title={title} />;
      }
    }
  };

  const urls = (metrics: Metric[], period: Period) => {
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
    metrics,
    10000,
  );

  const points = data;

  let hasError = error;
  if (previousError.current && !error) {
    hasError = previousError.current;
  }
  previousError.current = error;

  const loadingComponent = () => {
    if (type === chartTypes[0]) {
      return <MetricGaugeItem name={title} loading />;
    } else if (type == chartTypes[1]) {
      return <MetricNumberItem title={title} loading />;
    } else if (type == chartTypes[2]) {
      return <MetricNumbersItem title={title} loading />;
    }
  };

  const errorComponent = () => {
    if (type === chartTypes[0]) {
      return <MetricGaugeItem name={title} hasError={hasError} />;
    } else if (type == chartTypes[1]) {
      return <MetricNumberItem title={title} hasError={hasError} />;
    } else if (type == chartTypes[2]) {
      return <MetricNumbersItem title={title} hasError={hasError} />;
    }
  };

  return (
    <Box h="100%">
      {/* See Issue : https://github.com/apollographql/apollo-client/pull/4974 */}
      <FetchSuspense
        isLoading={isLoading || !points || typeof points[0] === "undefined"}
        error={hasError}
        loadingComponent={loadingComponent()}
        errorComponent={errorComponent()}
        points={points}
      >
        {({ points }) => displayWidget(points)}
      </FetchSuspense>
    </Box>
  );
};

export default WidgetDashboardItem;
