import { CSSProperties, FC } from "react";

import Panel from "./Panel";
import FaIcon from "./FaIcon";
import A from "./A";

type QueryErrorProps = {
  style?: CSSProperties;
  noBorder?: boolean;
};

const QueryError: FC<QueryErrorProps> = ({ style, noBorder }) => {
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

export default QueryError;
