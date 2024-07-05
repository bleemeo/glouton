import React from "react";

type ErrorBlockProps = {
  error: Array<string>;
};

const ErrorBlock: React.FC<ErrorBlockProps> = ({ error }) => {
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

export default ErrorBlock;
