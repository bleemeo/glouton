import React, { FC } from "react";
import { Loading } from "./Loading";

const Fallback: FC = () => {
  return (
    <div className="d-flex flex-row justify-content-center align-items-center">
      <Loading size="xl" message="Page is loading..." />
    </div>
  );
};

export default Fallback;
