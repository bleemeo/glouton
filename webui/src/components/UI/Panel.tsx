import React, { FC } from "react";
import Row from "react-bootstrap/Row";
import Col from "react-bootstrap/Col";
import { Box, Card, CardBody } from "@chakra-ui/react";

type PanelProps = {
  children: React.ReactNode;
  className?: string;
  xl?: number;
  md?: number;
  offset?: number;
};

const Panel: FC<PanelProps> = ({
  children,
  className = "",
  xl = 12,
  md = 12,
  offset = 0,
}) => (
  <Row>
    <Col xl={xl} md={md} offset={offset}>
      <Card m={2}>
        <Box mt="-0.8rem">
          <CardBody>
            <Box className={className}>{children}</Box>
          </CardBody>
        </Box>
      </Card>
    </Col>
  </Row>
);

export default Panel;
