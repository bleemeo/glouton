import React, { FC, useEffect, useRef, useState } from "react";
import * as d3 from "d3";
import { AxiosError } from "axios";

import Panel from "../UI/Panel";
import FetchSuspense from "../UI/FetchSuspense";

import { useHTTPDataFetch } from "../utils/hooks";
import { isNullOrUndefined } from "../utils";
import { badgeColorSchemeForStatus } from "../utils/converter";
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
  useDisclosure,
  Button,
  Dialog,
  Grid,
  GridItem,
  Tag as ChakraTag,
  Text,
  Flex,
  Link,
  Box,
  List,
  Spacer,
  Badge,
  Center,
  Wrap,
  WrapItem,
  Alert,
} from "@chakra-ui/react";
import ServiceDetails from "../Service/ServiceDetails";
import * as echarts from "echarts/core";
import { PieChart } from "echarts/charts";
import { EChartsOption } from "echarts";
import { CanvasRenderer } from "echarts/renderers";
import { LegendComponent, TooltipComponent } from "echarts/components";
import { Tooltip } from "../UI/tooltip";
import { LuExternalLink } from "react-icons/lu";
import { FaCheckCircle, FaChevronRight, FaInfoCircle } from "react-icons/fa";
import { IoIosWarning } from "react-icons/io";

echarts.use([PieChart, CanvasRenderer, TooltipComponent, LegendComponent]);

type AgentDetailsProps = {
  facts: Fact[];
};

const AgentDetails: FC<AgentDetailsProps> = ({ facts }) => {
  const [showServiceDetails, setShowServiceDetails] = useState<Service | null>(
    null,
  );

  const svgStatusChart = useRef<HTMLDivElement | null>(null);

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
        <Alert.Root status="error" w="fit-content">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>
              {" "}
              This agent is more than 60 days old. You should update it!{" "}
            </Alert.Title>
            <Alert.Description>
              {" "}
              See the{" "}
              <Link
                colorPalette="teal.500"
                href="https://go.bleemeo.com/l/agent-upgrade"
              >
                documentation <LuExternalLink />
              </Link>
              &nbsp; to learn how to do it.{" "}
            </Alert.Description>
          </Alert.Content>
        </Alert.Root>
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
  } = useHTTPDataFetch<Tag[]>(TAGS_URL, null, 60000);

  const {
    isLoading: isLoadingAgentInformation,
    error: errorAgentInformation,
    data: agentInformationData,
  } = useHTTPDataFetch<AgentInfo>(AGENT_INFORMATIONS_URL, null, 60000);

  const {
    isLoading: isLoadingAgentStatus,
    error: errorAgentStatus,
    data: agentStatusData,
  } = useHTTPDataFetch<AgentStatus[]>(AGENT_STATUS_URL, null, 60000);

  const isLoading: boolean =
    isLoadingServices ||
    isLoadingTags ||
    isLoadingAgentInformation ||
    isLoadingAgentStatus;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const error: AxiosError<unknown, any> | null =
    errorServices || errorTags || errorAgentInformation || errorAgentStatus;

  const services: Service | null = servicesData;
  const tags: Tag[] | null = tagsData;
  const agentInformation: AgentInfo | null = agentInformationData;
  const agentStatus: AgentStatus[] | null = agentStatusData;

  const problemsChart = (
    <div
      ref={svgStatusChart}
      style={{
        width: "100%",
        height: "49vh",
      }}
    />
  );

  let problemsBadges: JSX.Element | null = null;

  if (agentStatus) {
    // options for the echarts pie chart
    const agentOKCount = agentStatus.filter(
      (status) => status.status === 0,
    ).length;
    const agentWarningCount = agentStatus.filter(
      (status) => status.status === 1,
    ).length;
    const agentCriticalCount = agentStatus.filter(
      (status) => status.status === 2,
    ).length;

    // options for echarts pie chart
    const option: EChartsOption = {
      tooltip: {
        trigger: "item",
        formatter: "{a} <br/>{b} : {c} ({d}%)",
      },
      legend: {
        top: "5%",
        selectedMode: false,
        data: ["OK", "Warning", "Critical"],
      },
      series: [
        {
          name: "Status",
          type: "pie",
          top: "-200px",
          bottom: "-100px",
          radius: "55%",
          center: ["50%", "60%"],
          label: {
            show: false,
            position: "center",
          },
          data: [
            { value: agentOKCount, name: "OK" },
            { value: agentWarningCount, name: "Warning" },
            { value: agentCriticalCount, name: "Critical" },
          ],
          labelLine: {
            show: false,
          },
          color: ["#38a169", "#FFA500", "#e53e3e"],
          emphasis: {
            itemStyle: {
              shadowBlur: 10,
              shadowOffsetX: 0,
              shadowColor: "rgba(0, 0, 0, 0.5)",
            },
          },
        },
      ],
    };

    const svg = echarts.init(svgStatusChart.current);
    svg.setOption(option);

    const warningMessages = agentStatus
      .filter((status) => status.status === 1)
      .map((status) => status.statusDescription)
      .join("\n");

    const criticalMessages = agentStatus
      .filter((status) => status.status === 2)
      .map((status) => `${status.statusDescription} (${status.serviceName})`)
      .join("\n");

    if (warningMessages || criticalMessages) {
      problemsBadges = (
        <Box>
          {warningMessages ? (
            <Tooltip
              content={warningMessages}
              contentProps={{ fontSize: "md" }}
            >
              <Badge colorScheme="orange">Warning</Badge>
            </Tooltip>
          ) : null}
          {criticalMessages ? (
            <Tooltip
              content={criticalMessages}
              contentProps={{ fontSize: "md" }}
            >
              <Badge colorScheme="red">Critical</Badge>
            </Tooltip>
          ) : null}
        </Box>
      );
    }
  }

  useEffect(() => {
    const handleResize = () => {
      if (svgStatusChart.current) {
        const svg = echarts.init(svgStatusChart.current);
        svg.resize();
      }
    };
    window.addEventListener("resize", handleResize);
    return () => {
      window.removeEventListener("resize", handleResize);
    };
  }, []);

  const {
    open: isOpenModal,
    onOpen: onOpenModal,
    onClose: onCloseModal,
  } = useDisclosure();

  const serviceModal = (
    <Dialog.Root open={isOpenModal} onOpenChange={onCloseModal}>
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content>
          <Dialog.Header>{showServiceDetails?.name}</Dialog.Header>
          <Dialog.CloseTrigger />
          <Dialog.Body>
            {showServiceDetails ? (
              <ServiceDetails service={showServiceDetails} />
            ) : null}
          </Dialog.Body>

          <Dialog.Footer>
            <Button colorScheme="blue" mr={3} onClick={onCloseModal}>
              Close
            </Button>
          </Dialog.Footer>
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  );

  return (
    <>
      {serviceModal}

      {expireAgentBanner}

      <Grid
        h="100%"
        templateRows="repeat(6, 1fr)"
        templateColumns="repeat(9, 1fr)"
        gap={4}
      >
        <GridItem rowSpan={6} colSpan={3}>
          <Panel>
            <Box>
              <Text fontSize="xl" as="b">
                Information retrieved from the agent
              </Text>
              {factUpdatedAtDate ? (
                <Text>(last update: {factUpdatedAtDate.toLocaleString()})</Text>
              ) : null}
              <List.Root className="list-unstyled">
                {facts
                  .filter((f) => f.name !== "fact_updated_at")
                  .sort((a, b) => a.name.localeCompare(b.name))
                  .map((fact) => (
                    <List.Item key={fact.name}>
                      <List.Indicator asChild color="grey" fontSize={"2xs"}>
                        <FaChevronRight />
                      </List.Indicator>
                      <Text fontSize="md" as="b">
                        {fact.name}:
                      </Text>{" "}
                      {fact.value}
                    </List.Item>
                  ))}
              </List.Root>
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
                  <Wrap mt={2}>
                    {services
                      .filter((service) => service.active)
                      .sort((a, b) => a.name.localeCompare(b.name))
                      .map((service, idx) => {
                        return (
                          <WrapItem key={idx}>
                            <Button
                              ml={2}
                              mr={2}
                              colorPalette={badgeColorSchemeForStatus(
                                service.status,
                              )}
                              onClick={() => {
                                setShowServiceDetails(service);
                                onOpenModal();
                              }}
                            >
                              {service.name}
                              &nbsp;
                              <FaInfoCircle />
                            </Button>
                          </WrapItem>
                        );
                      })}
                  </Wrap>
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
                    <Wrap mt={2}>
                      {tags.map((tag, idx) => (
                        <WrapItem key={idx}>
                          <ChakraTag.Root w="fit-content" colorScheme="cyan">
                            <ChakraTag.Label>{tag.tagName}</ChakraTag.Label>
                          </ChakraTag.Root>
                        </WrapItem>
                      ))}
                    </Wrap>
                  ) : (
                    <Text fontSize="lg">No tags to display</Text>
                  )}
                </Flex>
              </Panel>
            )}
          </FetchSuspense>
        </GridItem>
        <GridItem colSpan={3} rowSpan={4}>
          <Panel>
            <Center flexDir="column">
              {problemsChart}
              <Spacer></Spacer>
              {problemsBadges}
            </Center>
          </Panel>
        </GridItem>
        <GridItem colSpan={3} rowSpan={4}>
          {agentInformation && Object.keys(agentInformation).length > 0 ? (
            <Panel>
              <Flex direction="column" justify="center" align="flex-start">
                {agentInformation.registrationAt &&
                new Date(agentInformation.registrationAt).getFullYear() !==
                  1 ? (
                  <Box>
                    <Text as="b">Glouton registration at:</Text>{" "}
                    {formatDateTimeWithSeconds(agentInformation.registrationAt)}
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
                    <FaCheckCircle color="green" />
                  ) : (
                    <IoIosWarning color="red" />
                  )}
                </Flex>
                <Box>
                  <Text as="b">
                    Need to troubleshoot ? &nbsp;
                    <Link
                      colorPalette="blue.500"
                      fontSize="xl"
                      href="/diagnostic"
                    >
                      /diagnostic
                    </Link>
                    &nbsp; may help you.
                  </Text>
                </Box>
                <Box>
                  <Text as="b">
                    Need more logs ? &nbsp;
                    <Link
                      colorPalette="blue.500"
                      fontSize="xl"
                      href="/diagnostic.txt/log.txt"
                    >
                      /log.txt
                    </Link>
                    &nbsp; may help you.
                  </Text>
                </Box>
              </Flex>
            </Panel>
          ) : null}
        </GridItem>
      </Grid>
    </>
  );
};

export default AgentDetails;
