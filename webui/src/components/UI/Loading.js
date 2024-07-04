import React from "react";
import PropTypes from "prop-types";

import FaIcon from "./FaIcon";

const Loading = ({ size = "l", message = "Loading" }) => {
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

Loading.propTypes = {
  size: PropTypes.string,
  message: PropTypes.string,
};

export default Loading;
