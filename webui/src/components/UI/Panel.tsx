import React from "react";
import { Grid, Card } from "tabler-react";

type PanelProps = {
  children: React.ReactNode;
  className?: string;
  xl?: number;
  md?: number;
  offset?: number;
  noBorder?: boolean;
};

const Panel: React.FC<PanelProps> = ({
  children,
  className = "",
  xl = 12,
  md = 12,
  offset = 0,
  noBorder = false,
}) => (
  <Grid.Row>
    <Grid.Col xl={xl} md={md} offset={offset}>
      <Card className={noBorder ? "no-border" : null}>
        <div style={{ marginTop: "-0.8rem" }}>
          <Card.Body>
            <div className={className}>{children}</div>
          </Card.Body>
        </div>
      </Card>
    </Grid.Col>
  </Grid.Row>
);

export default Panel;
