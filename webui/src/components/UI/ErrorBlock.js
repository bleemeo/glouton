import React from "react";
import PropTypes from "prop-types";

const ErrorBlock = ({ error }) => {
  if (!error) {
    return null;
  }
  if (error[0] === undefined) {
    return null;
  }

  return (
    <div className="card card-inverse card-danger text-center">
      <div className="card-block">
        <blockquote className="card-blockquote">
          {error.map((err) => (
            <p key={err} className="mb-0">
              {err.toString()}
            </p>
          ))}
        </blockquote>
      </div>
    </div>
  );
};

ErrorBlock.propTypes = {
  error: PropTypes.instanceOf(Array),
};

export default ErrorBlock;
