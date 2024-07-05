import React from "react";
import FaIcon from "./FaIcon";

type LoadingProps = {
  size?: string;
  message?: string;
};

const Loading: React.FC<LoadingProps> = ({
  size = "l",
  message = "Loading...",
}) => {
  const spin = <FaIcon icon="fas fa-sync fa-spin" />;
  switch (size) {
    case "s":
      return (
        <span>
          {spin} {message}
        </span>
      );
    case "m":
      return (
        <h4>
          {spin} {message}
        </h4>
      );
    case "xl":
      return (
        <h1>
          {spin} {message}
        </h1>
      );
    default:
      return (
        <h2>
          {spin} {message}
        </h2>
      );
  }
};

export default Loading;
