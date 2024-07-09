import React, { useEffect, useRef } from "react";
import WidgetDashboardItem from "../UI/WidgetDashboardItem";
import { chartTypes, useIntersection } from "../utils";
import MetricGaugeItem from "../Metric/MetricGaugeItem";
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

  const triggerRef = useRef<HTMLDivElement>(null);
  const isVisible = useIntersection(triggerRef, "0px");

  useEffect(() => {
    document.title = "Dashboard | Glouton";
  }, []);

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
            <div ref={triggerRef} className="col-sm-3" key={gaugeItem.title}>
              {isVisible ? (
                <WidgetDashboardItem
                  type={chartTypes[0]}
                  title={gaugeItem.title}
                  metrics={gaugeItem.metrics}
                  unit={gaugeItem.unit}
                  period={{ minutes: 60 }}
                />
              ) : (
                <MetricGaugeItem name={gaugeItem.title} loading />
              )}
            </div>
          ))}
        </div>
      </div>
    </>
  );
};

export default AgentSystemDashboard;
