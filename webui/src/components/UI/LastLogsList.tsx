import { FC } from "react";
import { useHTTPLogFetch } from "../utils/hooks";
import {
  Badge,
  Box,
  Code,
  Card,
  Flex,
  Link,
  Text,
  Icon,
} from "@chakra-ui/react";
import FetchSuspense from "./FetchSuspense";
import { Loading } from "../UI/Loading";
import QueryError from "./QueryError";
import { getHoursFromDateString } from "../utils/formater";
import { LOGS_URL } from "../utils/dataRoutes";
import { FaChevronRight } from "react-icons/fa";

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
            <Flex justifyContent="space-between" alignItems="center">
              <Text fontSize="xl" as="b">
                Latest logs
              </Text>
              <Text fontSize="sm">
                <Icon>
                  <FaChevronRight />
                </Icon>
                View{" "}
                <Link color="teal.500" href="/diagnostic.txt/log.txt">
                  full logs
                </Link>
              </Text>
            </Flex>
            <Box h="100%" overflowY="scroll">
              {logs.map((log, index) => (
                <Card.Root key={index}>
                  <Card.Body p={1}>
                    <Code fontSize="x-small">
                      <Badge>{getHoursFromDateString(log.timestamp)}</Badge>
                      &nbsp;
                      {log.message}
                    </Code>
                  </Card.Body>
                </Card.Root>
              ))}
            </Box>
          </>
        );
      }}
    </FetchSuspense>
  );
};
