/* eslint-disable camelcase */
import React, { FC, useState } from "react";
import * as d3 from "d3";
import "react-tooltip/dist/react-tooltip.css";
import { AxiosError } from "axios";

import Panel from "../UI/Panel";
import Smiley from "../UI/Smiley";
import FetchSuspense from "../UI/FetchSuspense";

import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined, Problems } from "../utils";
import { badgeColorSchemeForStatus, textForStatus } from "../utils/converter";
import { formatDateTimeWithSeconds } from "../utils/formater";
import {
  SERVICES_URL,
  AGENT_INFORMATIONS_URL,
  AGENT_STATUS_URL,
  TAGS_URL,
} from "../utils/dataRoutes";
import {
  AgentInfo,
  AgentStatus,
  Fact,
  Service,
  Tag,
} from "../Data/data.interface";
import {
  Modal,
  ModalOverlay,
  ModalContent,
  ModalCloseButton,
  useDisclosure,
  Button,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Container,
  Grid,
  GridItem,
  Tag as ChakraTag,
  Text,
  Flex,
  Link,
  Box,
  Alert,
  AlertDescription,
  AlertTitle,
  AlertIcon,
  ListItem,
  List,
  ListIcon,
  Spacer,
} from "@chakra-ui/react";
import ServiceDetails from "../Service/ServiceDetails";
import {
  CheckCircleIcon,
  ChevronRightIcon,
  InfoIcon,
  WarningIcon,
} from "@chakra-ui/icons";

type AgentDetailsProps = {
  facts: Fact[];
};

const AgentDetails: FC<AgentDetailsProps> = ({ facts }) => {
  const [showServiceDetails, setShowServiceDetails] = useState<Service | null>(
    null,
  );

  const factUpdatedAt: string | undefined = facts.find(
    (f: { name: string }) => f.name === "fact_updated_at",
  )?.value;

  let factUpdatedAtDate: Date | null = null;

  if (factUpdatedAt) {
    factUpdatedAtDate = new Date(factUpdatedAt);
  }

  let expireAgentBanner: JSX.Element | null = null;
  let agentDate: Date | null = null;

  const agentVersion: string | undefined = facts.find(
    (f: { name: string }) => f.name === "glouton_version",
  )?.value;

  if (agentVersion) {
    const expDate: Date = new Date();
    expDate.setDate(expDate.getDate() - 60);

    // First try new format (e.g. 18.03.21.134432)
    agentDate = d3.timeParse(".%L")(agentVersion.slice(0, 15));
    if (!agentDate) {
      // then old format (0.20180321.134432)
      agentDate = d3.timeParse(".%L")(agentVersion.slice(0, 17));
    }

    if (agentDate && agentDate < expDate) {
      expireAgentBanner = (
        <Alert status="error" w="fit-content">
          <AlertIcon />
          <AlertTitle>
            {" "}
            This agent is more than 60 days old. You should update it!{" "}
          </AlertTitle>
          <AlertDescription>
            {" "}
            See the{" "}
            <Link
              color="teal.500"
              href="https://go.bleemeo.com/l/agent-upgrade"
              isExternal
            >
              documentation
            </Link>
            &nbsp; to learn how to do it.{" "}
          </AlertDescription>
        </Alert>
      );
    }
  }

  const {
    isLoading: isLoadingServices,
    error: errorServices,
    data: servicesData,
  } = useHTTPDataFetch<Service>(SERVICES_URL, null, 60000);

  const {
    isLoading: isLoadingTags,
    error: errorTags,
    data: tagsData,
  } = useHTTPDataFetch<Tag>(TAGS_URL, null, 60000);

  const {
    isLoading: isLoadingAgentInformation,
    error: errorAgentInformation,
    data: agentInformationData,
  } = useHTTPDataFetch<AgentInfo>(AGENT_INFORMATIONS_URL, null, 60000);

  const {
    isLoading: isLoadingAgentStatus,
    error: errorAgentStatus,
    data: agentStatusData,
  } = useHTTPDataFetch<AgentStatus>(AGENT_STATUS_URL, null, 60000);

  const isLoading: boolean =
    isLoadingServices ||
    isLoadingTags ||
    isLoadingAgentInformation ||
    isLoadingAgentStatus;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const error: AxiosError<unknown, any> | null =
    errorServices || errorTags || errorAgentInformation || errorAgentStatus;

  const services: Service | null = servicesData;
  const tags: Tag | null = tagsData;
  const agentInformation: AgentInfo | null = agentInformationData;
  const agentStatus: AgentStatus | null = agentStatusData;

  let problems: JSX.Element | null = null;

  if (agentStatus) {
    problems = (
      <Box>
        <Flex
          direction="column"
          justify="center"
          id="agentStatus"
          align="center"
        >
          <Smiley status={agentStatus.status} />
          <Text textAlign="center" fontSize="xl" as="b">
            {textForStatus(agentStatus.status)}
          </Text>
        </Flex>
        {agentStatus.statusDescription ? (
          <Box>
            <Problems problems={agentStatus.statusDescription} />
          </Box>
        ) : null}
      </Box>
    );
  }

  const {
    isOpen: isOpenModal,
    onOpen: onOpenModal,
    onClose: onCloseModal,
  } = useDisclosure();

  const serviceModal = (
    <Modal isOpen={isOpenModal} onClose={onCloseModal}>
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{showServiceDetails?.name}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          {showServiceDetails ? (
            <ServiceDetails service={showServiceDetails} />
          ) : null}
        </ModalBody>

        <ModalFooter>
          <Button colorScheme="blue" mr={3} onClick={onCloseModal}>
            Close
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );

  return (
    <Container id="page-wrapper" mt={5}>
      {serviceModal}

      {expireAgentBanner}

      <Container>
        <Grid
          h="200px"
          templateRows="repeat(4, 1fr)"
          templateColumns="repeat(9, 1fr)"
          gap={4}
        >
          <GridItem rowSpan={4} colSpan={3}>
            <Panel>
              <Box>
                <Text fontSize="xl" as="b">
                  Information retrieved from the agent
                </Text>
                {factUpdatedAtDate ? (
                  <Text>
                    (last update: {factUpdatedAtDate.toLocaleString()})
                  </Text>
                ) : null}
                <List className="list-unstyled">
                  {facts
                    .filter((f) => f.name !== "fact_updated_at")
                    .sort((a, b) => a.name.localeCompare(b.name))
                    .map((fact) => (
                      <ListItem key={fact.name}>
                        <ListIcon as={ChevronRightIcon} color="grey.500" />
                        <Text as="b">{fact.name}:</Text> {fact.value}
                      </ListItem>
                    ))}
                </List>
              </Box>
            </Panel>
          </GridItem>
          <GridItem colSpan={3}>
            <FetchSuspense
              isLoading={isLoading}
              error={
                error ||
                isNullOrUndefined(services) ||
                isNullOrUndefined(tags) ||
                isNullOrUndefined(agentInformation) ||
                isNullOrUndefined(agentStatus)
              }
              services={services}
            >
              {({ services }) => (
                <Panel>
                  <Box>
                    <Text fontSize="xl" as="b">
                      Services running on this agent:
                    </Text>
                    <Flex mt={2}>
                      {services
                        .filter((service) => service.active)
                        .sort((a, b) => a.name.localeCompare(b.name))
                        .map((service, idx) => {
                          return (
                            <Button
                              key={idx}
                              ml={2}
                              mr={2}
                              colorScheme={badgeColorSchemeForStatus(
                                service.status,
                              )}
                              onClick={() => {
                                setShowServiceDetails(service);
                                onOpenModal();
                              }}
                            >
                              {service.name}
                              &nbsp;
                              <InfoIcon />
                            </Button>
                          );
                        })}
                    </Flex>
                  </Box>
                </Panel>
              )}
            </FetchSuspense>
          </GridItem>
          <GridItem colSpan={3}>
            <FetchSuspense
              isLoading={isLoading}
              error={
                error ||
                isNullOrUndefined(services) ||
                isNullOrUndefined(tags) ||
                isNullOrUndefined(agentInformation) ||
                isNullOrUndefined(agentStatus)
              }
              tags={tags}
            >
              {({ tags }) => (
                <Panel>
                  <Flex direction="column">
                    <Text fontSize="xl" as="b">
                      Tags for {facts.find((f) => f.name === "fqdn")?.value}:
                    </Text>
                    {tags.length > 0 ? (
                      <ul className="list-inline">
                        {tags.map((tag) => (
                          <ChakraTag
                            key={tag.id}
                            w="fit-content"
                            colorScheme="cyan"
                          >
                            {tag.name}
                          </ChakraTag>
                        ))}
                      </ul>
                    ) : (
                      <Text fontSize="lg">No tags to display</Text>
                    )}
                  </Flex>
                </Panel>
              )}
            </FetchSuspense>
          </GridItem>
          <GridItem colSpan={2} rowSpan={2}>
            {problems ? <Panel>{problems}</Panel> : null}
          </GridItem>
          <GridItem colSpan={4} rowSpan={2}>
            {agentInformation && Object.keys(agentInformation).length > 0 ? (
              <Panel>
                <Flex direction="column" justify="center" align="flex-start">
                  {agentInformation.registrationAt &&
                  new Date(agentInformation.registrationAt).getFullYear() !==
                    1 ? (
                    <Box>
                      <Text as="b">Glouton registration at:</Text>{" "}
                      {formatDateTimeWithSeconds(
                        agentInformation.registrationAt,
                      )}
                    </Box>
                  ) : null}
                  {agentInformation.lastReport &&
                  new Date(agentInformation.lastReport).getFullYear() !== 1 ? (
                    <Box>
                      <Text as="b">Glouton last report:</Text>{" "}
                      {formatDateTimeWithSeconds(agentInformation.lastReport)}
                    </Box>
                  ) : null}
                  <Flex align="center" justify="center">
                    <Text as="b">Connected to Bleemeo ? &nbsp; </Text>
                    <Spacer></Spacer>
                    {agentInformation.isConnected ? (
                      <CheckCircleIcon color="green.500" />
                    ) : (
                      <WarningIcon color="red.500" />
                    )}
                  </Flex>
                  <Box>
                    <Text as="b">
                      Need to troubleshoot ? &nbsp;
                      <Link
                        textColor="blue.500"
                        fontSize="xl"
                        href="/diagnostic"
                      >
                        /diagnostic
                      </Link>
                      &nbsp; may help you.
                    </Text>
                  </Box>
                </Flex>
              </Panel>
            ) : null}
          </GridItem>
        </Grid>
      </Container>
    </Container>
  );
};

export default AgentDetails;
