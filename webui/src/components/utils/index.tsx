import { isNil } from "lodash-es";
import React, { RefObject } from "react";
import { FaArrowDown, FaArrowUp, FaCheckCircle, FaMoon } from "react-icons/fa";

export const chartTypes = ["gauge", "number", "numbers"];
export const UNIT_FLOAT = 0;
export const UNIT_PERCENTAGE = 1;
export const UNIT_INT = 2;

export const isNullOrUndefined = (variable: unknown) => isNil(variable);

export type Period = {
  minutes: number;
  from?: Date;
  to?: Date;
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const isEmpty = (obj: any) => {
  for (const key in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) return false;
  }
  return true;
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export const useIntersection = (element: RefObject<any>, rootMargin) => {
  const [isVisible, setIsVisible] = React.useState(false);

  React.useEffect(() => {
    const current = element?.current;
    const observer = new IntersectionObserver(
      ([entry]) => {
        setIsVisible(entry.isIntersecting);
      },
      { rootMargin },
    );

    if (current) observer?.observe(current);

    return () => {
      if (current) {
        observer.unobserve(current);
      }
    };
  }, []);

  return isVisible;
};

export const iconFromName = (
  name: string,
  w: number,
  h: number,
  color: string,
) => {
  switch (name) {
    case "arrow-up":
      return <FaArrowUp width={w} height={h} color={color} />;
    case "arrow-down":
      return <FaArrowDown width={w} height={h} color={color} />;
    case "process-sleeping":
      return <FaMoon width={w} height={h} color={color} />;
    case "process-running":
      return <FaCheckCircle width={w} height={h} color={color} />;
    default:
      return <></>;
  }
};
