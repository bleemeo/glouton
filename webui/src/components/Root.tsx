import React from "react";
import {
  ChakraBaseProvider,
  extendBaseTheme,
  theme as chakraTheme,
} from "@chakra-ui/react";

import Routes from "./Routes";
import TopNavBar from "./App/TopNavBar";

const {
  Button,
  Modal,
  Badge,
  Card,
  Table,
  Tooltip,
  Tag,
  Select,
  Alert,
  Divider,
} = chakraTheme.components;

const theme = extendBaseTheme({
  components: {
    Button,
    Modal,
    Badge,
    Card,
    Table,
    Tooltip,
    Tag,
    Select,
    Alert,
    Divider,
  },
});

const Root = () => {
  return (
    <ChakraBaseProvider theme={theme}>
      <TopNavBar />
      <div className="main-content">
        <Routes />
      </div>
    </ChakraBaseProvider>
  );
};

export default Root;
