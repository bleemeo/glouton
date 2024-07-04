import React from "react";
import PropTypes from "prop-types";
import Panel from "./Panel";
import FaIcon from "./FaIcon";
import A from "./A";

const QueryError = ({ style, noBorder }) => {
  const errorElement = (
    <div
      className="d-flex flex-column justify-content-end align-items-center"
      style={{ height: "8rem", ...style }}
    >
      <h3>An error has just happened</h3>
      <p style={{ fontSize: "110%" }}>
        Press F5 or{" "}
        <A onClick={() => window.location.reload()}>
          <FaIcon icon="fa fa-sync" />
        </A>{" "}
        to reload the page
      </p>
    </div>
  );
  if (noBorder) return errorElement;
  else return <Panel>{errorElement}</Panel>;
};

QueryError.propTypes = {
  style: PropTypes.object,
  noBorder: PropTypes.bool,
};

export default QueryError;
