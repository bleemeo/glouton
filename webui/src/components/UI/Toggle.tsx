/* eslint-disable @typescript-eslint/no-explicit-any */
import { useEffect, useState, useRef, FC } from "react";

import FaIcon from "./FaIcon";
import { isNil } from "lodash-es";

type ToggleProps = {
  firstOption: string;
  secondOption: string;
  onClick: (option: number) => void;
  type?: "sm" | "md" | "lg";
  defaultOption?: 0 | 1;
};

const Toggle: FC<ToggleProps> = ({
  firstOption,
  secondOption,
  onClick,
  type = "md",
  defaultOption = 0,
}) => {
  const [option, setOption] = useState(defaultOption);
  const firstSpan = useRef<any | null>(null);
  const secondSpan = useRef<any | null>(null);
  const flap = useRef<any | null>(null);

  const navFlipStyle = (firstSpanOpacity, secondSpanOpacity) => {
    if (isNil(firstSpan.current) || isNil(secondSpan.current)) {
      return;
    }

    firstSpan.current.style.opacity = firstSpanOpacity;
    secondSpan.current.style.opacity = secondSpanOpacity;
  };

  useEffect(() => {
    if (defaultOption === 0) navFlipStyle(1, 0);
    else {
      navFlipStyle(0, 1);
      flap.current.classList.add("isFlipped");
    }
  }, []);
  useEffect(() => {
    if (option === 1) {
      onClick(1);
      setTimeout(() => {
        navFlipStyle(0, 1);
      }, 100);
      flap.current.classList.add("isFlipped");
    } else {
      onClick(0);
      setTimeout(() => {
        navFlipStyle(1, 0);
      }, 100);
      flap.current.classList.remove("isFlipped");
    }
  }, [option]);

  const liStyle: React.CSSProperties = {};

  switch (type) {
    case "sm":
      liStyle.padding = "3px 6px";
      break;
    case "lg":
      liStyle.padding = "10px 20px";
      break;
    default:
      liStyle.padding = "4px 12px";
      break;
  }

  const toggleWidth =
    (firstOption.length > secondOption.length
      ? firstOption.length
      : secondOption.length) *
      20 +
    40;

  return (
    <div id="toggleContainer">
      <div id="toggle" style={{ width: toggleWidth }}>
        <ul>
          <li style={liStyle} onClick={() => setOption(0)}>
            {firstOption}
          </li>
          <li style={liStyle} onClick={() => setOption(1)}>
            {secondOption}
          </li>
        </ul>
        <div id="navContainer">
          <div id="nav" ref={flap}>
            <span ref={firstSpan}>
              {firstOption}{" "}
              <small className="smaller text-success">
                <FaIcon icon="fa fa-check" />
              </small>
            </span>
            <span ref={secondSpan}>
              {secondOption}{" "}
              <small className="smaller text-success">
                <FaIcon icon="fa fa-check" />
              </small>
            </span>
          </div>
        </div>
      </div>
    </div>
  );
};

export default Toggle;
