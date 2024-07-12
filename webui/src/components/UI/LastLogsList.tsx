import React, { FC } from "react";
import { useHTTPLogFetch } from "../utils/hooks";
import { Badge, Box, Code, Card, CardBody, Flex, Link } from "@chakra-ui/react";
import FetchSuspense from "./FetchSuspense";
import Loading from "./Loading";
import QueryError from "./QueryError";
import { getHoursFromDateString } from "../utils/formater";
import { Text } from "@chakra-ui/react";
import { LOGS_URL } from "../utils/dataRoutes";
import { ChevronRightIcon } from "@chakra-ui/icons";

const LOGS_COUNT = 20;

export const LastLogsList: FC = () => {
  const { isLoading, error, logs } = useHTTPLogFetch(
    LOGS_URL,
    LOGS_COUNT,
    10000,
  );

  const loadingComponent = (
    <Box>
      <Loading size="xl" />;
    </Box>
  );

  const errorComponent = (
    <Box>
      <QueryError />;
    </Box>
  );

  return (
    <FetchSuspense
      isLoading={isLoading}
      error={error}
      loadingComponent={loadingComponent}
      errorComponent={errorComponent}
      logs={logs}
    >
      {({ logs }) => {
        return (
          <>
            <Flex justify="flex-start">
              <Text fontSize="xl" as="b">
                Latest logs
              </Text>
            </Flex>
            <Box h="100%" overflowY="scroll">
              {logs.map((log, index) => (
                <Card key={index}>
                  <CardBody p={1}>
                    <Code fontSize="x-small">
                      <Badge>{getHoursFromDateString(log.timestamp)}</Badge>
                      &nbsp;
                      {log.message}
                    </Code>
                  </CardBody>
                </Card>
              ))}
            </Box>
            <Flex mt={1} justify="flex-end">
              <Text fontSize="lg">
                <ChevronRightIcon />
                View{" "}
                <Link
                  fontWeight="700"
                  color="teal.500"
                  href="/diagnostic.txt/log.txt"
                >
                  full logs
                </Link>
              </Text>
            </Flex>
          </>
        );
      }}
    </FetchSuspense>
  );
};
