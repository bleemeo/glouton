import React from "react";
import {
  ChakraBaseProvider,
  extendBaseTheme,
  theme as chakraTheme,
} from "@chakra-ui/react";

import Routes from "./Routes";
import TopNavBar from "./App/TopNavBar";

const { Button, Modal, Badge } = chakraTheme.components;

const theme = extendBaseTheme({
  components: {
    Button,
    Modal,
    Badge,
  },
});

const Root = () => {
  return (
    <ChakraBaseProvider theme={theme}>
      <div className="marginOffset">
        <TopNavBar />
        <div className="main-content">
          <Routes />
        </div>
      </div>
    </ChakraBaseProvider>
  );
};

export default Root;
