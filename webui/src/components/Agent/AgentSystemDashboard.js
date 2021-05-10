import React, { useEffect, useState } from "react";
import VisibilitySensor from "react-visibility-sensor";
import PropTypes from "prop-types";
import WidgetDashboardItem from "../UI/WidgetDashboardItem";
import {
  chartTypes,
  UNIT_PERCENTAGE,
  UNIT_BYTE,
  UNIT_NUMBER,
  LabelName,
} from "../utils";
import { computeBackwardForward } from "../utils/ComputeBackwarForward";
import { formatDateTime } from "../utils/formater";
import EditPeriodModal, { lastQuickRanges } from "./EditPeriodModal";
import MetricGaugeItem from "../Metric/MetricGaugeItem";
import LineChart from "../UI/LineChart";
import { useWindowWidth } from "../utils/hooks";
import { getStorageItem, setStorageItem } from "../utils/storage";

var gaugesBar = [];

const gaugesBarPrometheus = [
  {
    title: "CPU",
    metrics: [
      '(1-sum(irate(node_cpu_seconds_total{mode="idle"}[1m]))/sum(irate(node_cpu_seconds_total[1m])))*100',
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: [
      "(node_memory_MemTotal_bytes - node_memory_MemFree_bytes - node_memory_SReclaimable_bytes - node_memory_Cached_bytes - node_memory_Buffers_bytes)/node_memory_MemTotal_bytes*100",
    ],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: ["irate(node_disk_io_time_seconds_total[1m])*100"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: [
      '(1-node_filesystem_avail_bytes{mountpoint="/"}/node_filesystem_size_bytes{mountpoint="/"})*100',
    ],
    unit: UNIT_PERCENTAGE,
  },
];

const gaugesBarBLEEMEO = [
  {
    title: "CPU",
    metrics: ["cpu_used"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "Memory",
    metrics: ["mem_used_perc"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "IO",
    metrics: ["io_utilization"],
    unit: UNIT_PERCENTAGE,
  },
  {
    title: "/",
    metrics: ["disk_used_perc"],
    mountpoint: "/",
    unit: UNIT_PERCENTAGE,
  },
];

var widgets = [];

const widgetsBLEEMEO = [
  { title: "Processor Usage", type: chartTypes[1], unit: UNIT_PERCENTAGE },
  { title: "Memory Usage", type: chartTypes[1], unit: UNIT_BYTE },
  {
    title: "Disk IO Utilization",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["io_utilization"],
  },
  {
    title: "Disk Read Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["io_read_bytes"],
  },
  {
    title: "Disk Write Bytes",
    type: chartTypes[2],
    unit: UNIT_BYTE,
    metrics: ["io_write_bytes"],
  },
  {
    title: "Disk Read Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["io_reads"],
  },
  {
    title: "Disk Write Number",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["io_writes"],
  },
  {
    title: "Network Packets",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["net_packets_recv", "net_packets_sent"],
  },
  {
    title: "Network Errors",
    type: chartTypes[2],
    unit: UNIT_NUMBER,
    metrics: ["net_err_in", "net_err_out"],
  },
  {
    title: "Disk Space",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["disk_used_perc"],
  },
  {
    title: "Swap Usage",
    type: chartTypes[2],
    unit: UNIT_PERCENTAGE,
    metrics: ["swap_used_perc"],
  },
];

const AgentSystemDashboard = ({ facts }) => {
  console.log();
  if (facts.find((f) => f.name === "metrics_format").value === "Bleemeo") {
    gaugesBar = gaugesBarBLEEMEO;
    widgets = widgetsBLEEMEO;
  } else if (
    facts.find((f) => f.name === "metrics_format").value == "Prometheus"
  ) {
    gaugesBar = gaugesBarPrometheus;
    widgets = widgetsBLEEMEO;
  }
  const [period, setPeriod] = useState(
    getStorageItem("period") || { minutes: 60 }
  );
  const [showEditPeriodMal, setShowEditPeriodMal] = useState(false);
  useEffect(() => {
    document.title = "Dashboard | Glouton";
  }, []);

  useEffect(() => {
    setStorageItem("period", period);
  }, [period]);

  const windowWidth = useWindowWidth();

  let periodText = "";
  if (period.minutes) {
    const range = lastQuickRanges.find((p) => p.value === period.minutes);
    if (range) periodText = range.label;
  } else if (period.from && period.to) {
    periodText =
      "from " +
      formatDateTime(period.from) +
      " to " +
      formatDateTime(period.to);
  }

  const onPeriodClick = () => setShowEditPeriodMal(true);

  const handleBackwardForwardFunc = (isForward = false) => {
    let startDate = new Date();
    let endDate = new Date();
    if (period.minutes) {
      startDate.setUTCMinutes(startDate.getUTCMinutes() - period.minutes);
      const res = computeBackwardForward(
        period.minutes,
        startDate,
        endDate,
        isForward
      );
      startDate = res.startDate;
      endDate = res.endDate;
    } else if (period.from && period.to) {
      const nbMinutes = Math.floor(
        Math.abs(new Date(period.to) - new Date(period.from)) / 1000 / 60
      );
      startDate = new Date(period.from);
      endDate = new Date(period.to);
      const res = computeBackwardForward(
        nbMinutes,
        startDate,
        endDate,
        isForward
      );
      startDate = res.startDate;
      endDate = res.endDate;
    }
    setPeriod({ from: startDate, to: endDate });
  };

  let editFromAndToModal = null;
  if (showEditPeriodMal) {
    editFromAndToModal = (
      <EditPeriodModal
        period={period}
        onPeriodChange={(newPeriod) => setPeriod(newPeriod)}
        onClose={() => setShowEditPeriodMal(false)}
      />
    );
  }

  let refetchTime = 10080;
  if (period.minutes) {
    refetchTime = period.minutes / 6;
  }

  return (
    <>
      {editFromAndToModal}
      <div className="row">
        <div className="col-xl-12">
          <div className="btn-toolbar float-right" id="copy">
            <button className="btn btn-outline-dark" onClick={onPeriodClick}>
              {periodText}
            </button>
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
          {widgets.map((widget) => (
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
