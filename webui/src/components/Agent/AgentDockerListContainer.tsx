import { FC, useEffect } from "react";

import Panel from "../UI/Panel";
import AgentDockerList from "./AgentDockerList";
import { Heading } from "@chakra-ui/react";

const AgentDockerListContainer: FC = () => {
  useEffect(() => {
    document.title = "Docker | Glouton";
  }, []);
  return (
    <Panel>
      <Heading>Docker</Heading>
      <AgentDockerList />
    </Panel>
  );
};

export default AgentDockerListContainer;
