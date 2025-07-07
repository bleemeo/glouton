import { FC } from "react";
import { Box, Card } from "@chakra-ui/react";

type PanelProps = {
  children: React.ReactNode;
  className?: string;
};

const Panel: FC<PanelProps> = ({ children, className = "" }) => (
  <Card.Root m={2}>
    <Box mt="-0.8rem">
      <Card.Body>
        <Box className={className}>{children}</Box>
      </Card.Body>
    </Box>
  </Card.Root>
);

export default Panel;
