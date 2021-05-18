import React, { useRef } from "react";
import PropTypes from "prop-types";
import MetricGaugeItem from "../Metric/MetricGaugeItem";
import { chartTypes, isShallowEqual } from "../utils";
import LineChart from "./LineChart";
import { httpFetch } from "../utils/hooks";
import FetchSuspense from "./FetchSuspense";

const WidgetDashboardItem = ({
  type,
  title,
  metrics,
  unit,
  period,
  refetchTime,
  handleBackwardForward,
  windowWidth,
}) => {
  const previousError = useRef(null);
  const handleBackwardForwardFunc = (isForward = false) => {
    handleBackwardForward(isForward);
  };

  const displayWidget = (points) => {
    switch (type) {
      case chartTypes[0]: {
        let lastPoint = null;
        if (points[0]) {
          lastPoint = parseFloat(
            points[0]["values"][points[0]["values"].length - 1][1]
          );
        }
        let thresholds = null;
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
            title={title}
            unit={unit}
            period={period}
            refetchTime={refetchTime}
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
            title={title}
            unit={unit}
            period={period}
            refetchTime={refetchTime}
            handleBackwardForward={handleBackwardForwardFunc}
            windowWidth={windowWidth}
          />
        );
      }
    }
  };

  const { isLoading, data, error } = httpFetch(
    {
      query: metrics,
      start: period.from
        ? new Date(period.from).toISOString()
        : new Date(
            new Date().setMinutes(new Date().getMinutes() - period.minutes)
          ).toISOString(),
      end: period.to
        ? new Date(period.to).toISOString()
        : new Date().toISOString(),
      step: 10,
    },
    10000
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
        isLoading={isLoading || !points}
        error={hasError}
        loadingComponent={
          type === chartTypes[0] ? (
            <MetricGaugeItem loading name={title} />
          ) : (
            <LineChart title={title} loading />
          )
        }
        fallbackComponent={
          type === chartTypes[0] ? (
            <MetricGaugeItem hasError={hasError} name={title} />
          ) : (
            <LineChart title={title} hasError={hasError} />
          ) /* eslint-disable-line react/jsx-indent */
        }
        points={points}
      >
        {({ points }) => displayWidget(points)}
      </FetchSuspense>
    </div>
  );
  /* let displayWidgetItem
  if (isLoading || !points) {
    switch (type) {
      case chartTypes[0]:
        displayWidgetItem = <MetricGaugeItem loading name={title} />
        break
      default:
        displayWidgetItem = <LineChart title={title} loading />
        break
    }
  } else if (hasError) {
    switch (type) {
      case chartTypes[0]:
        displayWidgetItem = <MetricGaugeItem hasError={hasError} name={title} />
        break
      default:
        displayWidgetItem = <LineChart title={title} hasError={hasError} />
        break
    }
  } else {
  } */
};

WidgetDashboardItem.propTypes = {
  type: PropTypes.string.isRequired,
  title: PropTypes.string.isRequired,
  metrics: PropTypes.any,
  mountpoins: PropTypes.string,
  labels: PropTypes.instanceOf(Array),
  unit: PropTypes.number,
  refetchTime: PropTypes.number.isRequired,
  period: PropTypes.object.isRequired,
  handleBackwardForward: PropTypes.func,
  windowWidth: PropTypes.number,
};

export default React.memo(
  WidgetDashboardItem,
  (prevProps, nextProps) =>
    isShallowEqual(nextProps.period, prevProps.period) &&
    prevProps.isVisible === nextProps.isVisible &&
    prevProps.windowWidth === nextProps.windowWidth
);
