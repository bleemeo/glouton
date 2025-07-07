import { Provider } from "./UI/provider";

import Routes from "./Routes";
import TopNavBar from "./App/TopNavBar";
import { Container, Theme } from "@chakra-ui/react";

const Root = () => {
  return (
    <Provider>
      <Theme height={"full"} width={"full"}>
        <TopNavBar />
        <Container fluid ps={"90px"} mt={"70px"} w={"100vw"} h={"100vh"}>
          <Routes />
        </Container>
      </Theme>
    </Provider>
  );
};

export default Root;
