import React, { FC } from "react";
import * as d3 from "d3";

import { bytesToString, formatDateTimeWithSeconds } from "../utils/formater";
import { Topinfo } from "../Data/data.interface";
import {
  Badge,
  Box,
  ColorSwatch,
  Flex,
  Progress,
  Stack,
  Table,
} from "@chakra-ui/react";
import { Tooltip } from "../UI/tooltip";
import { DataListItem, DataListRoot } from "../UI/data-list";

type ColorPillProps = {
  color: string;
  label: string;
};

const ColorPill: FC<ColorPillProps> = ({ color, label }) => {
  return (
    <>
      <ColorSwatch value={color} size={"2xs"} w={"2"} />
      {label}
    </>
  );
};

const formatUptime = (uptimeSeconds: number) => {
  const uptimeDays = Math.trunc(uptimeSeconds / (24 * 60 * 60));
  const uptimeHours = Math.trunc((uptimeSeconds % (24 * 60 * 60)) / (60 * 60));
  const uptimeMinutes = Math.trunc((uptimeSeconds % (60 * 60)) / 60);

  const textMinutes = uptimeDays > 1 ? "minutes" : "minute";
  const textHours = uptimeHours > 1 ? "hours" : "hour";
  const textDays = uptimeMinutes > 1 ? "days" : "day";

  let uptimeString: string;
  if (uptimeDays === 0 && uptimeHours === 0) {
    uptimeString = `${uptimeMinutes} ${textMinutes}`;
  } else if (uptimeDays === 0) {
    uptimeString = `${uptimeHours} ${textHours}`;
  } else {
    uptimeString = `${uptimeDays} ${textDays}, ${uptimeHours} ${textHours}`;
  }

  return uptimeString;
};

type AgentProcessesInfoProps = {
  top: Topinfo;
};

const AgentProcessesInfo: FC<AgentProcessesInfoProps> = ({ top }) => {
  if (
    !top ||
    !top.CPU ||
    !top.Memory ||
    !top.Swap ||
    !top.Loads ||
    !top.Processes
  ) {
    return <div>Error: No data available</div>;
  }

  const timeDate = new Date(top["Time"]);

  // sometimes the sum of all CPU percentage make more than 100%
  // which broke the bar, so we have to re-compute them

  const cpuTotal =
    top.CPU.System +
    top.CPU.User +
    top.CPU.Nice +
    top.CPU.IOWait +
    top.CPU.Idle;

  const cpuSystemPerc = (top.CPU.System / cpuTotal) * 100;
  const cpuUserPerc = (top.CPU.User / cpuTotal) * 100;
  const cpuNicePerc = (top.CPU.Nice / cpuTotal) * 100;
  const cpuWaitPerc = (top.CPU.IOWait / cpuTotal) * 100;
  const cpuIdlePerc = (top.CPU.Idle / cpuTotal) * 100;

  const cpuTooltipMsg =
    `${d3.format(".2r")(cpuSystemPerc)}% system` +
    ` ‒ ${d3.format(".2r")(cpuUserPerc)}% user` +
    ` ‒ ${d3.format(".2r")(cpuNicePerc)}% nice` +
    ` ‒ ${d3.format(".2r")(cpuWaitPerc)}% wait` +
    ` ‒ ${d3.format(".2r")(cpuIdlePerc)}% idle`;

  const memTotal =
    top.Memory.Used + top.Memory.Free + top.Memory.Buffers + top.Memory.Cached;

  const memUsed = top.Memory.Used;
  const memUsedPerc = (memUsed / memTotal) * 100;
  const memFreePerc = (top.Memory.Free / memTotal) * 100;
  const memBuffersPerc = (top.Memory.Buffers / memTotal) * 100;
  const memCachedPerc = (top.Memory.Cached / memTotal) * 100;

  const memTooltipMsg =
    `${bytesToString(memUsed * 1024)} used` +
    ` ‒ ${bytesToString(top.Memory.Buffers * 1024)} buffers` +
    ` ‒ ${bytesToString(top.Memory.Cached * 1024)} cached` +
    ` ‒ ${bytesToString(top.Memory.Free * 1024)} free`;

  const swapUsedPerc = (top.Swap.Used / top.Swap.Total) * 100;
  const swapFreePerc = (top.Swap.Free / top.Swap.Total) * 100;

  const swapTooltipMsg =
    `${bytesToString(top.Swap.Used * 1024)} used` +
    ` ‒ ${bytesToString(top.Swap.Free * 1024)} free`;
  const maxLoad = Math.max(...top.Loads);

  return (
    <div className="row">
      <div className="col-lg-8">
        <Table.Root size={"sm"} variant="line" w="100%">
          <Table.Body>
            <Tooltip content={cpuTooltipMsg} aria-label="CPU tooltip">
              <Table.Row>
                <Table.Cell style={{ width: "10%" }}>
                  <strong>Cpu(s):</strong>
                </Table.Cell>
                <Table.Cell>
                  <Stack direction={"row"} w={"100%"} gap={0}>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="orange"
                      width={cpuSystemPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="blue"
                      width={cpuUserPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="cyan"
                      width={cpuNicePerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="red"
                      width={cpuWaitPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="green"
                      width={cpuIdlePerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                  </Stack>
                </Table.Cell>
              </Table.Row>
            </Tooltip>
            <Tooltip content={memTooltipMsg} aria-label="Memory tooltip">
              <Table.Row>
                <Table.Cell style={{ width: "10%" }}>
                  <strong>Mem:</strong>
                </Table.Cell>
                <Table.Cell>
                  <Stack direction={"row"} w={"100%"} gap={0}>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="blue"
                      width={memUsedPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="orange"
                      width={memBuffersPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="yellow"
                      width={memCachedPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="green"
                      width={memFreePerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                  </Stack>
                </Table.Cell>
              </Table.Row>
            </Tooltip>
            <Tooltip content={swapTooltipMsg} aria-label="Swap tooltip">
              <Table.Row>
                <Table.Cell style={{ width: "10%" }}>
                  <strong>Swap:</strong>
                </Table.Cell>
                <Table.Cell>
                  <Stack direction={"row"} w={"100%"} gap={0}>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="blue"
                      width={swapUsedPerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                    <Progress.Root
                      value={100}
                      size={"xl"}
                      shape={"square"}
                      colorPalette="green"
                      width={swapFreePerc + "%"}
                    >
                      <Progress.Track>
                        <Progress.Range />
                      </Progress.Track>
                    </Progress.Root>
                  </Stack>
                </Table.Cell>
              </Table.Row>
            </Tooltip>
          </Table.Body>
        </Table.Root>
      </div>
      <div className="col-lg-4">
        <DataListRoot orientation={"horizontal"} variant={"bold"} gap={1}>
          <DataListItem
            label="Last update"
            value={formatDateTimeWithSeconds(timeDate)}
          />
          <DataListItem label="Users" value={top.Users} />
          <DataListItem label="Uptime" value={formatUptime(top.Uptime)} />
          <DataListItem
            label="Load average"
            value={
              <Tooltip
                content={
                  <DataListRoot orientation={"horizontal"} size={"sm"} gap={1}>
                    <DataListItem
                      label={<ColorPill color="blue" label="Load 5" />}
                      value={top.Loads[0]}
                    />
                    <DataListItem
                      label={<ColorPill color="orange" label="Load 10" />}
                      value={top.Loads[1]}
                    />
                    <DataListItem
                      label={<ColorPill color="green" label="Load 15" />}
                      value={top.Loads[2]}
                    />
                  </DataListRoot>
                }
              >
                <Box flexGrow={1}>
                  <Progress.Root
                    value={(top.Loads[0] / maxLoad) * 100}
                    shape={"square"}
                    size={"sm"}
                    colorPalette="blue"
                  >
                    <Progress.Track>
                      <Progress.Range />
                    </Progress.Track>
                  </Progress.Root>

                  <Progress.Root
                    value={(top.Loads[1] / maxLoad) * 100}
                    shape={"square"}
                    size={"sm"}
                    colorPalette="orange"
                  >
                    <Progress.Track>
                      <Progress.Range />
                    </Progress.Track>
                  </Progress.Root>

                  <Progress.Root
                    value={(top.Loads[2] / maxLoad) * 100}
                    shape={"square"}
                    size={"sm"}
                    colorPalette="green"
                  >
                    <Progress.Track>
                      <Progress.Range />
                    </Progress.Track>
                  </Progress.Root>
                </Box>
              </Tooltip>
            }
          />
          <DataListItem
            label="Tasks"
            value={
              <Flex gap={1} wrap={"wrap"}>
                <Badge
                  rounded={"md"}
                  textTransform={"initial"}
                  colorPalette={"blue"}
                >
                  {top.Processes.length} total
                </Badge>
                <Badge
                  rounded={"md"}
                  textTransform={"initial"}
                  colorPalette={"green"}
                >
                  {top.Processes.filter((p) => p.status === "running").length}{" "}
                  running
                </Badge>
                <Badge
                  rounded={"md"}
                  textTransform={"initial"}
                  colorPalette={"orange"}
                >
                  {
                    top.Processes.filter(
                      (p) =>
                        p.status === "sleeping" ||
                        p.status === "?" ||
                        p.status === "idle" ||
                        p.status === "disk-sleep",
                    ).length
                  }{" "}
                  sleeping
                </Badge>
                <Badge
                  rounded={"md"}
                  textTransform={"initial"}
                  colorPalette={"red"}
                >
                  {top.Processes.filter((p) => p.status === "stopped").length}{" "}
                  stopped
                </Badge>
                <Badge
                  rounded={"md"}
                  textTransform={"initial"}
                  colorPalette={"black"}
                >
                  {top.Processes.filter((p) => p.status === "zombie").length}{" "}
                  zombie
                </Badge>
              </Flex>
            }
          />
        </DataListRoot>
      </div>
    </div>
  );
};

export default AgentProcessesInfo;
