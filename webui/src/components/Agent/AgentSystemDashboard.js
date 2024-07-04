import React, { useEffect, useState } from "react";
import VisibilitySensor from "react-visibility-sensor";
import PropTypes from "prop-types";
import WidgetDashboardItem from "../UI/WidgetDashboardItem";
import { chartTypes } from "../utils";
import { computeBackwardForward } from "../utils/ComputeBackwarForward";
import MetricGaugeItem from "../Metric/MetricGaugeItem";
import LineChart from "../UI/LineChart";
import { useWindowWidth } from "../utils/hooks";
import { setStorageItem } from "../utils/storage";
import {
  gaugesBarBLEEMEO,
  gaugesBarPrometheusLinux,
  gaugesBarPrometheusWindows,
  widgetsBLEEMEO,
  widgetsPrometheusLinux,
  widgetsPrometheusWindows,
} from "../Metric/DefaultDashboardMetrics";

const AgentSystemDashboard = ({ facts }) => {
  let gaugesBar = [];
  if (facts.find((f) => f.name === "metrics_format").value === "Bleemeo") {
    gaugesBar = gaugesBarBLEEMEO;
  } else if (
    facts.find((f) => f.name === "metrics_format").value == "Prometheus"
  ) {
    if (facts.find((f) => f.name === "kernel").value == "Linux") {
      gaugesBar = gaugesBarPrometheusLinux;
    } else {
      gaugesBar = gaugesBarPrometheusWindows;
    }
  }
  const [period, setPeriod] = useState({ minutes: 60 });
  //  const [showEditPeriodMal, setShowEditPeriodMal] = useState(false);
  useEffect(() => {
    document.title = "Dashboard | Glouton";
  }, []);

  useEffect(() => {
    setStorageItem("period", period);
  }, [period]);
  const windowWidth = useWindowWidth();

  const handleBackwardForwardFunc = (isForward = false) => {
    let startDate = new Date();
    let endDate = new Date();
    if (period.minutes) {
      startDate.setUTCMinutes(startDate.getUTCMinutes() - period.minutes);
      const res = computeBackwardForward(
        period.minutes,
        startDate,
        endDate,
        isForward,
      );
      startDate = res.startDate;
      endDate = res.endDate;
    } else if (period.from && period.to) {
      const nbMinutes = Math.floor(
        Math.abs(new Date(period.to) - new Date(period.from)) / 1000 / 60,
      );
      startDate = new Date(period.from);
      endDate = new Date(period.to);
      const res = computeBackwardForward(
        nbMinutes,
        startDate,
        endDate,
        isForward,
      );
      startDate = res.startDate;
      endDate = res.endDate;
    }
    setPeriod({ from: startDate, to: endDate });
  };

  //  let editFromAndToModal = null;
  //  if (showEditPeriodMal) {
  //    editFromAndToModal = (
  //      <EditPeriodModal
  //        period={period}
  //        onPeriodChange={(newPeriod) => setPeriod(newPeriod)}
  //        onClose={() => setShowEditPeriodMal(false)}
  //      />
  //    );
  //  }

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
                        refetchTime={refetchTime}
                        period={period}
                      />
                    );
                  } else {
                    return <MetricGaugeItem title={gaugeItem.title} loading />;
                  }
                }}
              </VisibilitySensor>
            </div>
          ))}
          {/* {widgets.map((widget) => (
            <div
              className="col-sm-12"
              style={{ marginTop: "1rem" }}
              key={widget.title}
            >
              <VisibilitySensor
                partialVisibility
                offset={{ top: 20, bottom: 20 }}
                scrollCheck
                intervalCheck
                intervalDelay={10000}
              >
                {(renderProps) => {
                  if (renderProps.isVisible) {
                    return (
                      <WidgetDashboardItem
                        type={widget.type}
                        title={widget.title}
                        refetchTime={refetchTime}
                        period={period}
                        unit={widget.unit}
                        metrics={widget.metrics}
                        handleBackwardForward={handleBackwardForwardFunc}
                        windowWidth={windowWidth}
                      />
                    );
                  } else {
                    return <LineChart title={widget.title} loading />;
                  }
                }}
              </VisibilitySensor>
            </div>
          ))} */}
        </div>
      </div>
    </>
  );
};

AgentSystemDashboard.propTypes = {
  facts: PropTypes.instanceOf(Array).isRequired,
};

export default AgentSystemDashboard;
