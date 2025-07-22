import * as d3 from "d3";
import { FC } from "react";
import { getColors } from "../utils/colors";

const tau = Math.PI * 1.6;
const size = 100;

const radius = size / 2;
const innerRadius = radius - radius / 2;
const strokeWidth = radius / 3;
const outerRadius = radius - strokeWidth;

const backgroundArc = d3.arc()({
  innerRadius: innerRadius,
  outerRadius: outerRadius,
  startAngle: 0,
  endAngle: tau,
});

const createArcPath = (x: number, total: number) => {
  return d3.arc()({
    innerRadius: innerRadius,
    outerRadius: outerRadius,
    startAngle: total,
    endAngle: total + x,
  });
};

type DonutPieChartProps = {
  value: number;
  segmentsColor: string[];
  segmentsStep: number[];
  fontSize: number;
  formattedValue: string | string[];
};

const DonutPieChart: FC<DonutPieChartProps> = ({
  value,
  segmentsColor,
  segmentsStep,
  fontSize,
  formattedValue,
}) => {
  const arcCmpts: JSX.Element[] = [];
  let totalValuesTau = 0.0;
  let previousStep = 0;
  let textCmpt: JSX.Element | undefined;

  // segmentsStep indicates the number of steps that compose gauge
  // For example, if you have [25, 50, 100]
  // That you have a path from 0 to 25, a path from 25 to 50 and a path from 50 to 100
  segmentsStep.forEach((item, idx) => {
    if (typeof item === "number" && !isNaN(item)) {
      const tauValues: { value: number; isTransparent: boolean }[] = [];
      // These conditions are here to give transparency for each path
      if (value > previousStep) {
        if (value < item) {
          // We have to build two paths if the value is between two steps
          tauValues.push({
            value: ((value - previousStep) * tau) / 100,
            isTransparent: false,
          });
          tauValues.push({
            value: ((item - value) * tau) / 100,
            isTransparent: true,
          });
        } else {
          tauValues.push({
            value: ((item - previousStep) * tau) / 100,
            isTransparent: false,
          });
        }
      } else {
        tauValues.push({
          value: ((item - previousStep) * tau) / 100,
          isTransparent: true,
        });
      }
      tauValues.forEach((tauValue, idxTau) => {
        const arc =
          createArcPath(tauValue.value, totalValuesTau) === null
            ? undefined
            : createArcPath(tauValue.value, totalValuesTau);
        const opacity = tauValue.isTransparent ? "55" : "ff";
        arcCmpts.push(
          <path
            key={idx.toString() + idxTau.toString()}
            //fill={segmentsColor[idx] + opacity}
            style={
              segmentsColor[idx] ? { fill: segmentsColor[idx] + opacity } : {}
            }
            d={arc!}
          />,
        );
        totalValuesTau += tauValue.value;
      });
      previousStep = item;
    }
  });

  if (typeof value === "number" && !isNaN(value)) {
    textCmpt = (
      <text
        textAnchor="middle"
        style={{
          dominantBaseline: "middle",
          fontWeight: "bold",
          fill: getColors("colors.text"),
          fontSize: `${fontSize}px`,
        }}
        transform="rotate(144)"
      >
        {formattedValue}
      </text>
    );
  }

  return (
    <div className="block-fullsize d-flex justify-content-center">
      <svg
        className="opacityTransition"
        viewBox={`0 0 ${size} ${size - 10}`}
        width="60%"
        height="60%"
        style={{ display: "block" }}
      >
        <g transform={`translate(${radius},${radius}) scale(1.5) rotate(216)`}>
          <path className="gaugeBg" d={backgroundArc!} />
          {arcCmpts.map((arcCmpt) => arcCmpt)}
          {textCmpt}
        </g>
      </svg>
    </div>
  );
};

export default DonutPieChart;
