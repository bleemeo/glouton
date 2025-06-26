import React, { FC } from "react";
import { Service } from "../Data/data.interface";
import { Table, Box, Text } from "@chakra-ui/react";
import { Loading } from "../UI/Loading";
import { useHTTPDataFetch } from "../utils/hooks";
import { SERVICES_URL } from "../utils/dataRoutes";
import QueryError from "./QueryError";
import FetchSuspense from "./FetchSuspense";
import { Tooltip } from "./tooltip";
import { FaMinus } from "react-icons/fa";

export const ServicesList: FC = () => {
  const {
    data: services,
    error,
    isLoading,
  } = useHTTPDataFetch<Service[]>(SERVICES_URL, {}, 100000);

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
      services={services}
    >
      {(data: { services: Service[] }) => {
        const services = data.services;
        return (
          <>
            <Text fontSize="xl" as="b">
              Services
            </Text>
            {/* <TableContainer w="100%"> */}
            <Table.Root variant="line" w="100%">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeader>Service</Table.ColumnHeader>
                  <Table.ColumnHeader>IP Address</Table.ColumnHeader>
                  <Table.ColumnHeader>Instance</Table.ColumnHeader>
                  <Table.ColumnHeader>Exe path</Table.ColumnHeader>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {services
                  ? services.map((service) => (
                      <Table.Row key={service.name}>
                        <Table.Cell>
                          <Text pb={0} as="b">
                            {service.name}
                          </Text>
                        </Table.Cell>
                        <Table.Cell>{service.ipAddress}</Table.Cell>
                        <Table.Cell>
                          {service.containerId ? (
                            <Tooltip
                              content={service.containerId}
                              aria-label="ContainerID tooltip"
                            >
                              <Text
                                overflow="hidden"
                                textOverflow="ellipsis"
                                whiteSpace="nowrap"
                                mb={0}
                                fontSize="xs"
                              >
                                {service.containerId}
                              </Text>
                            </Tooltip>
                          ) : (
                            <FaMinus />
                          )}
                        </Table.Cell>
                        <Table.Cell maxW={0}>
                          <Tooltip
                            content={service.exePath}
                            aria-label="Exe path tooltip"
                          >
                            <Text
                              overflow="hidden"
                              textOverflow="ellipsis"
                              whiteSpace="nowrap"
                              mb={0}
                              fontSize="xs"
                            >
                              {service.exePath}
                            </Text>
                          </Tooltip>
                        </Table.Cell>
                      </Table.Row>
                    ))
                  : null}
              </Table.Body>
            </Table.Root>
            {/* </TableContainer> */}
          </>
        );
      }}
    </FetchSuspense>
  );
};
