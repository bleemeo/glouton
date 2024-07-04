import React, { useEffect, useRef } from "react";
import PropTypes from "prop-types";

import { twoDigitsWithMetricPrefix } from "../utils/formater";

const SIZE = 100;

const MetricNumberItem = ({ value, title, titleFontSize = 30 }) => {
  const svgElem = useRef(null);
  const textElem = useRef(null);

  const resize = () => {
    if (svgElem.current && textElem.current) {
      const svg = svgElem.current;
      const svgCTM = svg.getScreenCTM();
      const textBBox = textElem.current.getBBox();
      const svgHeight = svg.parentNode.clientHeight / svgCTM.a;
      const svgWidth = svg.parentNode.clientWidth / svgCTM.a;
      let textHeight = textBBox.height;
      if (textHeight === 0) {
        textHeight = 1;
      }
      let textWidth = textBBox.width;
      if (textWidth === 0) {
        textWidth = 1;
      }

      const ratio = Math.min(svgHeight / textHeight, svgWidth / textWidth);
      const halfSize = SIZE / 2;
      const translateRatio = -halfSize * (ratio - 1);
      textElem.current.setAttribute(
        "transform",
        `matrix(${ratio}, 0, 0, ${ratio}, ${translateRatio}, ${translateRatio})`,
      );
    }
  };

  useEffect(() => {
    resize();
  });

  let formattedValue = twoDigitsWithMetricPrefix(value);
  if (formattedValue === undefined) {
    formattedValue = "\u00A0\u00A0\u00A0";
  }

  return (
    <div className="card card-body">
      <div className="d-flex justify-content-center">
        <div className="w-100 h-100 d-flex flex-column flex-nowrap justify-content-center align-items-center">
          <div className="w-100 h-100">
            <svg
              viewBox={`0 0 ${SIZE} ${SIZE}`}
              width="100%"
              height="100%"
              ref={svgElem}
            >
              <text
                ref={textElem}
                x={SIZE / 2}
                y={SIZE / 2}
                textAnchor="middle"
                fill="currentColor"
                style={{ dominantBaseline: "middle", fontWeight: "bold" }}
              >
                {formattedValue}
              </text>
            </svg>
          </div>
          <div>
            <b style={{ fontSize: titleFontSize, wordBreak: "break-word" }}>
              {title}
            </b>
          </div>
        </div>
      </div>
    </div>
  );
};

MetricNumberItem.propTypes = {
  value: PropTypes.number.isRequired,
  title: PropTypes.oneOfType([PropTypes.object, PropTypes.string]).isRequired,
  titleFontSize: PropTypes.number,
};

export default MetricNumberItem;
