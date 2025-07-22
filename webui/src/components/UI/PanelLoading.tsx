import { FC } from "react";

import FaIcon from "./FaIcon";

const PanelLoading: FC = () => {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        justifyContent: "center",
        alignItems: "center",
        marginTop: "10vh",
      }}
    >
      <img
        src="/static/img/favicon.png"
        alt="Logo"
        height="160px"
        style={{ borderRadius: "80px" }}
      />
      <h1>
        <FaIcon icon="fa fa-sync fa-spin" /> Your Glouton App is loading...
      </h1>
    </div>
  );
};

export default PanelLoading;
