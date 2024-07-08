import React from "react";
import Row from "react-bootstrap/Row";
import Col from "react-bootstrap/Col";
import Card from "react-bootstrap/Card";

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
  <Row>
    <Col xl={xl} md={md} offset={offset}>
      <Card
        className={noBorder ? "no-border" : undefined}
        style={{ margin: "4px" }}
      >
        <div style={{ marginTop: "-0.8rem" }}>
          <Card.Body>
            <div className={className}>{children}</div>
          </Card.Body>
        </div>
      </Card>
    </Col>
  </Row>
);

export default Panel;
