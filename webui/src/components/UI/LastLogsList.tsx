import React, { FC, useState } from "react";
import { useHTTPLogFetch } from "../utils/hooks";
import {
  Badge,
  Box,
  Code,
  Card,
  CardBody,
  Select,
  Flex,
} from "@chakra-ui/react";
import FetchSuspense from "./FetchSuspense";
import Loading from "./Loading";
import QueryError from "./QueryError";
import { getHoursFromDateString } from "../utils/formater";
import { Text } from "@chakra-ui/react";

export const LastLogsList: FC = () => {
  const [logsCount, setLogsCount] = useState(10);
  const { isLoading, error, logs } = useHTTPLogFetch(logsCount, 10000);

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

  const options = [
    { label: "10", value: 10 },
    { label: "20", value: 20 },
    { label: "30", value: 30 },
    { label: "40", value: 40 },
    { label: "50", value: 50 },
  ];

  const handleOptionChange = (event: React.ChangeEvent<HTMLSelectElement>) => {
    setLogsCount(parseInt(event.target.value));
  };

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
            <Flex justify="space-between">
              <Text fontSize="xl" as="b">
                Last logs
              </Text>
              <Select
                placeholder="Select option"
                value={logsCount}
                onChange={handleOptionChange}
                w="fit-content"
                size="sm"
              >
                {options.map((option, index) => (
                  <option key={index} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </Select>
            </Flex>
            <Box h="100%" overflow="scroll">
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
          </>
        );
      }}
    </FetchSuspense>
  );
};
