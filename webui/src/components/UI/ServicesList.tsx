import React, { FC } from "react";
import { Service } from "../Data/data.interface";
import {
  TableContainer,
  Table,
  Thead,
  Th,
  Tr,
  Tbody,
  Box,
  Text,
  Td,
  Tooltip,
} from "@chakra-ui/react";
import Loading from "./Loading";
import { useHTTPDataFetch } from "../utils/hooks";
import { SERVICES_URL } from "../utils/dataRoutes";
import QueryError from "./QueryError";
import FetchSuspense from "./FetchSuspense";
import { MinusIcon } from "@chakra-ui/icons";

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
            <TableContainer w="100%">
              <Table variant="simple" w="100%">
                <Thead>
                  <Tr>
                    <Th>Service</Th>
                    <Th>IP Address</Th>
                    <Th>Instance</Th>
                    <Th>Exe path</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {services
                    ? services.map((service) => (
                        <Tr key={service.name}>
                          <Td>
                            <Text pb={0} as="b">
                              {service.name}
                            </Text>
                          </Td>
                          <Td>{service.ipAddress}</Td>
                          <Td>
                            {service.containerId ? (
                              <Tooltip
                                label={service.containerId}
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
                              <MinusIcon />
                            )}
                          </Td>
                          <Td maxW={0}>
                            <Tooltip
                              label={service.exePath}
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
                          </Td>
                        </Tr>
                      ))
                    : null}
                </Tbody>
              </Table>
            </TableContainer>
          </>
        );
      }}
    </FetchSuspense>
  );
};
