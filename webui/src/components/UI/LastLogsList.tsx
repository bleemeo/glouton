import React, { FC } from "react";
import { useHTTPLogFetch } from "../utils/hooks";
import { Badge, Box, Code, Card, CardBody } from "@chakra-ui/react";
import FetchSuspense from "./FetchSuspense";
import Loading from "./Loading";
import QueryError from "./QueryError";
import { getHoursFromDateString } from "../utils/formater";

type LastLogsListProps = {
  limit: number;
};

export const LastLogsList: FC<LastLogsListProps> = ({ limit }) => {
  const { isLoading, error, logs } = useHTTPLogFetch(limit, 10000);
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
        console.log("logs", logs);
        return (
          <Box>
            {logs.map((log, index) => (
              <Card key={index}>
                <CardBody p={1}>
                  <Code fontSize="x-small">
                    <Badge>{getHoursFromDateString(log.timestamp)}</Badge>&nbsp;
                    {log.message}
                  </Code>
                </CardBody>
              </Card>
            ))}
          </Box>
        );
      }}
    </FetchSuspense>
  );
};
