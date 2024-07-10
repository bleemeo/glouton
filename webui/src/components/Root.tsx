import React from "react";
import {
  ChakraBaseProvider,
  extendBaseTheme,
  theme as chakraTheme,
} from "@chakra-ui/react";

import Routes from "./Routes";
import TopNavBar from "./App/TopNavBar";

const { Button, Modal, Badge, Card, Table, Tooltip, Tag, Select, Alert } =
  chakraTheme.components;

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
