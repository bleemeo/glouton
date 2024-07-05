/* eslint-disable @typescript-eslint/no-unused-vars */
import React, { useEffect, useState } from "react";
import VisibilitySensor from "react-visibility-sensor";
import PropTypes from "prop-types";
import WidgetDashboardItem from "../UI/WidgetDashboardItem";
import { chartTypes } from "../utils";
import { computeBackwardForward } from "../utils/ComputeBackwarForward";
import MetricGaugeItem from "../Metric/MetricGaugeItem";
import { useWindowWidth } from "../utils/hooks";
import { setStorageItem } from "../utils/storage";
import {
  GaugeBar,
  gaugesBarBLEEMEO,
  gaugesBarPrometheusLinux,
  gaugesBarPrometheusWindows,
} from "../Metric/DefaultDashboardMetrics";
import { Fact } from "../Data/data.interface";

type AgentSystemDashboardProps = {
  facts: Fact[];
};

type Period = {
  minutes: number;
  from?: Date;
  to?: Date;
};

const AgentSystemDashboard = ({ facts }: AgentSystemDashboardProps) => {
  let gaugesBar: GaugeBar[] = [];

  if (facts.find((f) => f.name === "metrics_format")?.value === "Bleemeo") {
    gaugesBar = gaugesBarBLEEMEO;
  } else if (
    facts.find((f) => f.name === "metrics_format")?.value == "Prometheus"
  ) {
    if (facts.find((f) => f.name === "kernel")?.value == "Linux") {
      gaugesBar = gaugesBarPrometheusLinux;
    } else {
      gaugesBar = gaugesBarPrometheusWindows;
    }
  }
  const [period, setPeriod] = useState<Period>({ minutes: 60 });

  useEffect(() => {
    document.title = "Dashboard | Glouton";
  }, []);

  useEffect(() => {
    setStorageItem("period", period);
  }, [period]);

  let refetchTime = 10080;
  if (period.minutes) {
    refetchTime = period.minutes / 6;
  }

  return (
    <>
      <div className="row">
        <div className="col-xl-12">
          <div className="btn-toolbar float-right" id="copy">
            <div className="btn btn-outline-dark">Last 1 hour</div>
          </div>
        </div>
      </div>
      <div className="marginOffset">
        <div className="row">
          {gaugesBar.map((gaugeItem) => (
            <div className="col-sm-3" key={gaugeItem.title}>
              <VisibilitySensor
                partialVisibility
                offset={{ top: -460, bottom: -460 }}
                scrollCheck
                intervalCheck
                intervalDelay={10000}
                resizeCheck
              >
                {(renderProps) => {
                  if (renderProps.isVisible) {
                    return (
                      <WidgetDashboardItem
                        type={chartTypes[0]}
                        title={gaugeItem.title}
                        metrics={gaugeItem.metrics}
                        unit={gaugeItem.unit}
                        period={period}
                      />
                    );
                  } else {
                    return <MetricGaugeItem name={gaugeItem.title} loading />;
                  }
                }}
              </VisibilitySensor>
            </div>
          ))}
        </div>
      </div>
    </>
  );
};

AgentSystemDashboard.propTypes = {
  facts: PropTypes.instanceOf(Array).isRequired,
};

export default AgentSystemDashboard;
