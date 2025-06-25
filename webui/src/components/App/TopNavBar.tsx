import React, { FC } from "react";
import { useHTTPDataFetch } from "../utils/hooks";
import FetchSuspense from "../UI/FetchSuspense";
import { FACTS_URL } from "../utils/dataRoutes";
import { Fact } from "../Data/data.interface";
import { Box, Heading } from "@chakra-ui/react";

const TopNavBar: FC = () => {
  const {
    isLoading,
    error,
    data: facts,
  } = useHTTPDataFetch<Fact[]>(FACTS_URL, null, 10000);

  return (
    <Box
      position="fixed"
      top="0"
      left="0"
      right="0"
      zIndex="1000"
      boxShadow="sm"
      bg="background"
    >
      <FetchSuspense
        isLoading={isLoading}
        error={error}
        facts={facts}
        fallbackComponent={<></>}
      >
        {({ facts }) => (
          <Heading textAlign={"right"} pr={5}>
            {facts ? facts.find((f: Fact) => f.name === "fqdn").value : ""}
          </Heading>
        )}
      </FetchSuspense>
    </Box>
  );
};

export default TopNavBar;
