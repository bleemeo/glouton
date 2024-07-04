import React from "react";
import Routes from "./Routes";
import TopNavBar from "./App/TopNavBar";

const Root = () => {
  return (
      <div className="marginOffset">
        <TopNavBar />
        <div className="main-content">
          <Routes />
        </div>
      </div>
  );
};

export default Root;
