import { FC, useState } from "react";

import FaIcon from "./FaIcon";
import A from "./A";

type PanelErrorBoundaryProps = {
  children: React.ReactNode;
};

const PanelErrorBoundary: FC<PanelErrorBoundaryProps> = ({ children }) => {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const [hasError, setHasError] = useState(false);

  if (hasError) {
    return (
      <div
        style={{
          display: "flex",
          flexFlow: "column wrap",
          justifyContent: "space-between",
          alignItems: "center",
        }}
      >
        <div
          style={{
            flexShrink: 1,
            display: "flex",
            flexFlow: "column wrap",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <h1
            style={{
              padding: "3rem 0",
              fontSize: "600%",
              fontWeight: "bold",
            }}
          >
            An error has just happened
          </h1>
          <p style={{ fontSize: "140%" }}>
            Please reload the page by clicking this icon{" "}
            <A onClick={() => window.location.reload()}>
              <FaIcon icon="fas fa-sync" />
            </A>{" "}
            or by pressing F5
          </p>
        </div>
      </div>
    );
  }
  return children;
};

export default PanelErrorBoundary;
