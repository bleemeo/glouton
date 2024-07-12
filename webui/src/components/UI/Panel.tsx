import React, { FC } from "react";
import { Box, Card, CardBody } from "@chakra-ui/react";

type PanelProps = {
  children: React.ReactNode;
  className?: string;
};

const Panel: FC<PanelProps> = ({ children, className = "" }) => (
  <Card m={2}>
    <Box mt="-0.8rem">
      <CardBody>
        <Box className={className}>{children}</Box>
      </CardBody>
    </Box>
  </Card>
);

export default Panel;
