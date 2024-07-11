import * as d3 from "d3";
import React, { FC, useState } from "react";

import FaIcon from "../UI/FaIcon";
import A from "../UI/A";
import DonutPieChart from "../UI/DonutPieChart";
import FetchSuspense from "../UI/FetchSuspense";
import ProcessesTable from "../UI/ProcessesTable";

import {
  _formatCpuTime,
  formatDateTime,
  formatToBits,
  formatToBytes,
  unitFormatCallback,
} from "../utils/formater";
import { useHTTPDataFetch } from "../utils/hooks";
import { PROCESSES_URL } from "../utils/dataRoutes";
import { Container, Process, Topinfo } from "../Data/data.interface";
import { colorForStatus } from "../utils/converter";
import { UNIT_PERCENTAGE } from "../utils";
import {
  Button,
  Center,
  Code,
  Flex,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text,
  useDisclosure,
} from "@chakra-ui/react";

type DockerProcessesProps = {
  containerId: string;
  name: string;
};

const DockerProcesses: FC<DockerProcessesProps> = ({ containerId, name }) => {
  const {
    isLoading,
    error,
    data: processes,
  } = useHTTPDataFetch<Topinfo>(PROCESSES_URL, { search: containerId }, 10000);

  return (
    // className="d-flex flex-column align-items-center mt-3"
    // style={{ minHeight: "10rem" }}
    <Flex direction="column" pl={2} justify="center" align="center">
      <FetchSuspense isLoading={isLoading} error={error} processes={processes}>
        {({ processes }) => {
          const result = processes;
          const dockerProcesses =
            result && result["Processes"] ? result["Processes"] : [];
          if (!dockerProcesses || dockerProcesses.length === 0) {
            return (
              <Text textAlign="center" fontSize="2xl">
                There are no processes related to{" "}
                <Code colorScheme="cyan">{name}</Code>
              </Text>
            );
          } else {
            const memTotal = result["Memory"]["Total"];
            const finalProcesses = dockerProcesses.map((process: Process) => {
              return {
                ...process,
                mem_percent: d3.format(".2r")(
                  (process.memory_rss / memTotal) * 100,
                ),
                new_cpu_times: _formatCpuTime(process.cpu_time),
              };
            });
            return (
              <Center flexDirection="column" overflow="auto" w="100%">
                <Text fontSize="xl" as="b">
                  Processes
                </Text>
                <ProcessesTable
                  data={finalProcesses}
                  sizePage={10}
                  classNames="dockerTable"
                  renderLoadMoreButton={false}
                />
              </Center>
            );
          }
        }}
      </FetchSuspense>
    </Flex>
  );
};

type DockerProps = {
  container: Container;
  date: React.ReactNode;
};

interface DockerInspect {
  name: string;
  inspect: string;
}

const Docker: FC<DockerProps> = ({ container, date }) => {
  const [dockerInspect, setDockerInspect] = useState<DockerInspect | null>(
    null,
  );
  const [showProcesses, setShowProcesses] = useState<boolean>(false);

  const renderDonutDocker = (name: string, value: number) => (
    <div className="small-widget">
      <div className="content">
        <DonutPieChart
          value={value}
          fontSize={15}
          segmentsColor={["#" + colorForStatus(0)]}
          segmentsStep={[100]}
          formattedValue={
            unitFormatCallback(UNIT_PERCENTAGE)(value)
              ? unitFormatCallback(UNIT_PERCENTAGE)(value)!
              : "N/A"
          }
        />
      </div>
      <div className="title">{name}</div>
    </div>
  );

  const renderNetwork = (
    name: string,
    sentValue: number,
    recvValue: number,
  ) => {
    const formattedSentValue =
      sentValue !== null ? formatToBits(sentValue) : null;
    const formattedRecvValue =
      recvValue !== null ? formatToBits(recvValue) : null;
    if (!formattedSentValue && !formattedRecvValue) {
      return (
        <div className="small-widget">
          <div className="content wide">
            <div className="content-row">
              <p style={{ fontSize: "80%", paddingTop: "15%" }}>N/A</p>
            </div>
          </div>
          <div className="title">{name}</div>
        </div>
      );
    } else {
      return (
        <div className="small-widget">
          <div className="content wide">
            <div className="content-row">
              {formattedSentValue ? (
                <span>
                  {formattedSentValue[0]}
                  <small>
                    &nbsp;
                    {formattedSentValue[1]}
                    /s sent
                  </small>
                </span>
              ) : null}
            </div>
            <div className="content-row">
              {formattedRecvValue ? (
                <span>
                  {formattedRecvValue[0]}
                  <small>
                    &nbsp;
                    {formattedRecvValue[1]}
                    /s receive
                  </small>
                </span>
              ) : null}
            </div>
          </div>
          <div className="title">{name}</div>
        </div>
      );
    }
  };

  const renderDisk = (name: string, writeValue: number, readValue: number) => {
    const formattedWriteValue =
      writeValue !== null ? formatToBytes(writeValue) : null;
    const formattedReadValue =
      readValue !== null ? formatToBytes(readValue) : null;
    if (!formattedReadValue && !formattedWriteValue) {
      return (
        <div className="small-widget">
          <div className="content wide">
            <div className="content-row">
              <p style={{ fontSize: "80%", paddingTop: "15%" }}>N/A</p>
            </div>
          </div>
          <div className="title">{name}</div>
        </div>
      );
    } else {
      return (
        <div className="small-widget">
          <div className="content wide">
            <div className="content-row">
              {formattedWriteValue !== null &&
              formattedWriteValue !== undefined ? (
                <span>
                  {formattedWriteValue[0]}
                  <small>
                    &nbsp;
                    {formattedWriteValue[1]}
                    /s write
                  </small>
                </span>
              ) : null}
            </div>
            <div className="content-row">
              {formattedReadValue !== null &&
              formattedReadValue !== undefined ? (
                <span>
                  {formattedReadValue[0]}
                  <small>
                    &nbsp;
                    {formattedReadValue[1]}
                    /s read
                  </small>
                </span>
              ) : null}
            </div>
          </div>
          <div className="title">{name}</div>
        </div>
      );
    }
  };

  const {
    isOpen: isOpenModal,
    onOpen: onOpenModal,
    onClose: onCloseModal,
  } = useDisclosure();
  const dockerModal = dockerInspect ? (
    <>
      <Modal size="xl" isOpen={isOpenModal} onClose={onCloseModal}>
        <ModalOverlay />
        <ModalContent>
          <ModalHeader>{dockerInspect?.name}</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <pre
              style={{
                maxHeight: "60vh",
                overflowY: "auto",
              }}
            >
              {JSON.stringify(JSON.parse(dockerInspect.inspect), null, 2)}
            </pre>
          </ModalBody>

          <ModalFooter>
            <Button colorScheme="blue" mr={3} onClick={onCloseModal}>
              Close
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  ) : null;

  return (
    <>
      {dockerModal}

      <div className="dockerItem list-group-item">
        <div
          className={`item-left-border ${
            container.state === "running" ? "success" : ""
          }`}
        >
          <span className="vertical-text">{container.state.toUpperCase()}</span>
        </div>
        <div className="row flex1 align-items-center justify-content-between px-3">
          <div className="col-xl-3 col-md-6">
            <h3 className="overflow-ellipsis" title={container.name}>
              {container.name}
            </h3>
            <small>{container.id.substring(0, 12)}</small>
            <br />
            <small>
              <a onClick={() => setShowProcesses(!showProcesses)}>
                {showProcesses ? (
                  <>
                    <FaIcon icon="fa fa-caret-down" /> Close Processes
                  </>
                ) : (
                  <>
                    <FaIcon icon="fa fa-caret-right" /> Show Processes
                  </>
                )}
              </a>
            </small>
          </div>
          <div className="col-xl-6 pull-xl-3 col-sm-12">
            <div className="blee-row">
              {renderDonutDocker("Memory", container.memUsedPerc)}
              {renderDonutDocker("CPU", container.cpuUsedPerc)}
              {renderNetwork(
                "Network IO",
                container.netBitsRecv,
                container.netBitsSent,
              )}
              {renderDisk(
                "Disk IO",
                container.ioWriteBytes,
                container.ioReadBytes,
              )}
            </div>
          </div>
          <div className="col-xl-3 push-xl-5 col-md-6 blee-row">
            <div style={{ minWidth: 0 }}>
              <small>
                <strong>Created&nbsp;at:</strong>
                &nbsp;
                {formatDateTime(container.createdAt)} {date}
                <br />
                <strong>Image&nbsp;name:</strong>
                &nbsp;
                {container.image}
                <br />
                <div className="overflow-ellipsis">
                  <strong>Cmd:</strong>
                  &nbsp;
                  <span title={container.command}>{container.command}</span>
                </div>
              </small>
            </div>
          </div>
          <div
            style={{
              position: "absolute",
              top: "0.5rem",
              right: "0.5rem",
              width: "unset",
              paddingRight: "unset",
            }}
          >
            <A
              onClick={() => {
                setDockerInspect({
                  name: container.name,
                  inspect: container.inspectJSON,
                });
                onOpenModal();
              }}
            >
              <FaIcon icon="fa fa-search-plus fa-2x" />
            </A>
          </div>
        </div>
        <div>
          {showProcesses ? (
            <DockerProcesses containerId={container.id} name={container.name} />
          ) : null}
        </div>
      </div>
    </>
  );
};

export default Docker;
