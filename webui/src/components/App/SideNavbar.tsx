import { FC, Suspense } from "react";
import { Link } from "react-router-dom";

import Fallback from "../UI/Fallback";
import PanelErrorBoundary from "../UI/PanelErrorBoundary";
import { Box, Icon, Spacer, VStack } from "@chakra-ui/react";
import { FaCircleInfo, FaDocker, FaGauge, FaMicrochip } from "react-icons/fa6";
import { ColorModeButton } from "../UI/color-mode";

const SideNavBar: FC = () => (
  <PanelErrorBoundary>
    <Suspense fallback={<Fallback />}>
      <Box
        position="fixed"
        top="0"
        left="0"
        height="100vh"
        p={4}
        boxShadow="sm"
        zIndex="1001" // Ensure it is above Top navbar
        bg="sidebarbackground"
      >
        <VStack gap={6} h={"100%"}>
          <Link to="/">
            <img src="/img/favicon.png" style={{ height: "2.2rem" }} />
          </Link>
          <Link to="/dashboard" title="Dashboard">
            <Icon as={FaGauge} boxSize={8} color="white" />
          </Link>
          <Link to="/docker" title="Docker">
            <Icon as={FaDocker} boxSize={8} color="white" />
          </Link>
          <Link to="/processes" title="Processes">
            <Icon as={FaMicrochip} boxSize={8} color="white" />
          </Link>
          <Link to="/informations" title="Informations">
            <Icon as={FaCircleInfo} boxSize={8} color="white" />
          </Link>
          <Spacer />
          <ColorModeButton variant={"plain"} color={"white"} />
        </VStack>
      </Box>
    </Suspense>
  </PanelErrorBoundary>
);

export default SideNavBar;
