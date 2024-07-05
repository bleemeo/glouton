import React from "react";
import DonutPieChart from "../UI/DonutPieChart";
import { unitFormatCallback } from "../utils/formater";
import Loading from "../UI/Loading";
import QueryError from "../UI/QueryError";
import { colorForStatus } from "../utils/converter";

type MetricGaugeItemProps = {
  unit?: number;
  value?: number;
  name: string;
  style?: object;
  fontSize?: number;
  titleFontSize?: number;
  loading?: boolean;
  hasError?: object;
  thresholds?: {
    highWarning?: number;
    highCritical?: number;
  };
};

const MetricGaugeItem = ({
  unit,
  value,
  name,
  style,
  fontSize,
  titleFontSize = 30,
  loading,
  hasError,
  thresholds,
}: MetricGaugeItemProps) => {
  if (loading) {
    return (
      <div className="card card-body widgetLoading" style={style}>
        <div className="d-flex flex-column flex-nowrap justify-content-center align-items-center">
          <Loading size="xl" />
        </div>
      </div>
    );
  } else if (hasError) {
    return (
      <div className="card card-body widgetError" style={style}>
        <div className="d-flex flex-column flex-nowrap justify-content-center align-items-center">
          <QueryError noBorder style={{ textAlign: "center" }} />
        </div>
      </div>
    );
  }
  const segmentsStep: number[] = [0];
  const segmentsColor = ["#" + colorForStatus(0)];

  if (thresholds) {
    if (thresholds.highWarning) {
      segmentsStep.push(thresholds.highWarning);
      segmentsColor.push("#" + colorForStatus(1));
    }
    if (thresholds.highCritical) {
      segmentsStep.push(thresholds.highCritical);
      segmentsColor.push("#" + colorForStatus(2));
    }
  }

  segmentsStep.push(100);
  segmentsColor.push("#" + colorForStatus(3));

  return (
    <div className="card card-body widget" style={style}>
      <div
        className="d-flex flex-column flex-nowrap justify-content-center align-items-center"
        style={{ height: "100%" }}
      >
        <DonutPieChart
          value={value ? value : 0}
          fontSize={fontSize ? fontSize : 12}
          segmentsStep={segmentsStep}
          segmentsColor={segmentsColor}
          formattedValue={
            unitFormatCallback(unit)(value)
              ? unitFormatCallback(unit)(value)!
              : "N/A"
          }
        />
        <div>
          <b style={{ fontSize: titleFontSize, textOverflow: "ellipsis" }}>
            {name}
          </b>
        </div>
      </div>
    </div>
  );
};

export default MetricGaugeItem;
