import {
  Box,
  Code,
  Drawer,
  HStack,
  IconButton,
  Link as ChakraLink,
  Portal,
  SimpleGrid,
  Text,
  VStack,
} from "@chakra-ui/react";
import { LuChartLine, LuX } from "react-icons/lu";
import { Link as RouterLink } from "react-router-dom";

import { usePromQLRange } from "../api/hooks";
import type { Container } from "../api/types";
import { StatusBadge, type Status } from "../app/StatusBadge";
import { formatBytes, formatPercent } from "../dashboard/format";
import { samplesOf } from "../dashboard/promql";
import { Sparkline } from "../dashboard/Sparkline";

type Props = {
  container: Container | null;
  requestedId: string | null;
  onClose: () => void;
};

function stateToStatus(state: string): Status {
  const s = state.toLowerCase();

  if (s.includes("running") || s === "up") return "ok";
  if (s.includes("paused") || s.includes("restarting")) return "warn";
  if (s.includes("exited") || s.includes("dead") || s.includes("stopped"))
    return "crit";

  return "unknown";
}

function relativeTime(iso: string | undefined): string {
  if (!iso) return "—";

  const t = new Date(iso).getTime();
  const zero = new Date("0001-01-01T00:00:00Z").getTime();

  if (!isFinite(t) || t === zero) return "—";

  const diffSec = Math.floor((Date.now() - t) / 1000);

  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;

  return `${Math.floor(diffSec / 86400)}d ago`;
}

function absoluteTime(iso: string | undefined): string {
  if (!iso) return "—";

  const t = new Date(iso).getTime();
  const zero = new Date("0001-01-01T00:00:00Z").getTime();

  if (!isFinite(t) || t === zero) return "—";

  return new Date(t).toLocaleString();
}

export function ContainerDrawer({ container, requestedId, onClose }: Props) {
  const open = container !== null || requestedId !== null;

  return (
    <Drawer.Root
      open={open}
      onOpenChange={(d) => {
        if (!d.open) onClose();
      }}
      size="md"
      placement="end"
    >
      <Portal>
        <Drawer.Backdrop bg="blackAlpha.500" />
        <Drawer.Positioner>
          <Drawer.Content
            bg="surface.panel"
            borderLeftWidth="1px"
            borderColor="border.subtle"
          >
            {container ? (
              <ContainerDetails container={container} onClose={onClose} />
            ) : (
              <RemovedNotice id={requestedId} onClose={onClose} />
            )}
          </Drawer.Content>
        </Drawer.Positioner>
      </Portal>
    </Drawer.Root>
  );
}

function ContainerDetails({
  container: c,
  onClose,
}: {
  container: Container;
  onClose: () => void;
}) {
  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle" pb="3">
        <HStack justify="space-between" align="start">
          <VStack align="start" gap="2">
            <StatusBadge status={stateToStatus(c.state)} label={c.state} />
            <Drawer.Title
              fontSize="lg"
              fontWeight="semibold"
              wordBreak="break-all"
            >
              {c.name}
            </Drawer.Title>
            <Text fontSize="xs" color="fg.subtle" fontFamily="mono">
              {c.id.slice(0, 24)}
            </Text>
          </VStack>
          <IconButton
            aria-label="Close"
            size="sm"
            variant="ghost"
            onClick={onClose}
          >
            <LuX />
          </IconButton>
        </HStack>
      </Drawer.Header>

      <Drawer.Body>
        <VStack align="stretch" gap="5" pt="2">
          <ChakraLink
            as={RouterLink as never}
            // @ts-expect-error react-router Link expects "to", chakra Link expects "href"
            to={`/containers/${encodeURIComponent(c.name)}`}
            onClick={onClose}
            display="inline-flex"
            alignItems="center"
            gap="2"
            px="3"
            py="2"
            fontSize="sm"
            fontWeight="medium"
            borderRadius="md"
            borderWidth="1px"
            borderColor="border.default"
            color="fg.default"
            bg="surface.subtle"
            _hover={{ bg: "surface.canvas", textDecoration: "none" }}
            w="fit-content"
          >
            <LuChartLine />
            View charts
          </ChakraLink>

          <SimpleGrid columns={2} gap="3">
            <SparkField
              label="CPU"
              value={formatPercent(c.cpuUsedPerc)}
              metric="container_cpu_used"
              containerName={c.name}
              color="#4F8DF5"
            />
            <SparkField
              label="Memory"
              value={formatPercent(c.memUsedPerc)}
              metric="container_mem_used_perc"
              containerName={c.name}
              color="#8B5CF6"
              yMax={100}
            />
          </SimpleGrid>

          <SimpleGrid columns={2} gap="3">
            <Field
              label="↓ Network"
              value={`${formatBytes(c.netBitsRecv / 8)}/s`}
              mono
            />
            <Field
              label="↑ Network"
              value={`${formatBytes(c.netBitsSent / 8)}/s`}
              mono
            />
            <Field
              label="Read"
              value={`${formatBytes(c.ioReadBytes)}/s`}
              mono
            />
            <Field
              label="Write"
              value={`${formatBytes(c.ioWriteBytes)}/s`}
              mono
            />
          </SimpleGrid>

          {c.primaryAddress || c.listenAddresses.length > 0 ? (
            <Section label="Addresses">
              <VStack align="stretch" gap="1.5">
                {c.primaryAddress ? (
                  <HStack gap="2" fontSize="xs" fontFamily="mono">
                    <Text color="fg.subtle" minW="6ch">
                      IP
                    </Text>
                    <Code variant="surface" fontSize="xs" px="2" py="0.5">
                      {c.primaryAddress}
                    </Code>
                  </HStack>
                ) : null}
                {c.listenAddresses.length > 0 ? (
                  <HStack gap="2" align="start" fontSize="xs" fontFamily="mono">
                    <Text color="fg.subtle" minW="6ch" pt="1">
                      Listen
                    </Text>
                    <HStack gap="1.5" wrap="wrap" flex="1">
                      {c.listenAddresses.map((addr) => (
                        <Code
                          key={addr}
                          variant="surface"
                          fontSize="xs"
                          px="2"
                          py="0.5"
                        >
                          {addr}
                        </Code>
                      ))}
                    </HStack>
                  </HStack>
                ) : null}
              </VStack>
            </Section>
          ) : null}

          <Section label="Image">
            <Code
              variant="surface"
              fontFamily="mono"
              fontSize="xs"
              wordBreak="break-all"
              p="2"
            >
              {c.image}
            </Code>
          </Section>

          {c.command ? (
            <Section label="Command">
              <Code
                variant="surface"
                fontFamily="mono"
                fontSize="xs"
                wordBreak="break-all"
                whiteSpace="pre-wrap"
                p="3"
              >
                {c.command}
              </Code>
            </Section>
          ) : null}

          <SimpleGrid columns={{ base: 1, sm: 2 }} gap="3">
            <Field
              label="Started"
              value={absoluteTime(c.startedAt)}
              hint={relativeTime(c.startedAt)}
            />
            <Field
              label="Created"
              value={absoluteTime(c.createdAt)}
              hint={relativeTime(c.createdAt)}
            />
            {c.finishedAt && relativeTime(c.finishedAt) !== "—" ? (
              <Field
                label="Finished"
                value={absoluteTime(c.finishedAt)}
                hint={relativeTime(c.finishedAt)}
              />
            ) : null}
            <Field label="Container ID" value={c.id} mono />
          </SimpleGrid>
        </VStack>
      </Drawer.Body>
    </>
  );
}

function RemovedNotice({
  id,
  onClose,
}: {
  id: string | null;
  onClose: () => void;
}) {
  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle">
        <HStack justify="space-between">
          <Drawer.Title>Container removed</Drawer.Title>
          <IconButton
            aria-label="Close"
            size="sm"
            variant="ghost"
            onClick={onClose}
          >
            <LuX />
          </IconButton>
        </HStack>
      </Drawer.Header>
      <Drawer.Body>
        <Text color="fg.muted" mt="4">
          {id
            ? `Container ${id.slice(0, 12)} is no longer in the list.`
            : "The selected container is no longer available."}
        </Text>
      </Drawer.Body>
    </>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <VStack align="stretch" gap="1.5">
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        {label}
      </Text>
      {children}
    </VStack>
  );
}

function escapeLabelValue(v: string): string {
  return v.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

const SPARK_RANGE_SECONDS = 15 * 60;
const SPARK_STEP_SECONDS = 30;

function SparkField({
  label,
  value,
  metric,
  containerName,
  color,
  yMax,
}: {
  label: string;
  value: string;
  metric: string;
  containerName: string;
  color: string;
  yMax?: number;
}) {
  const query = `${metric}{item="${escapeLabelValue(containerName)}"}`;
  const res = usePromQLRange(query, SPARK_RANGE_SECONDS, SPARK_STEP_SECONDS);
  const samples = samplesOf(res.data?.data?.result?.[0]);

  return (
    <VStack
      align="start"
      gap="1"
      bg="surface.subtle"
      borderRadius="md"
      borderWidth="1px"
      borderColor="border.subtle"
      p="3"
    >
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        {label}
      </Text>
      <Text
        fontSize="sm"
        fontFamily="mono"
        fontVariantNumeric="tabular-nums"
        fontWeight="medium"
      >
        {value}
      </Text>
      <Box w="full" mt="1">
        {samples.length > 0 ? (
          <Sparkline data={samples} color={color} yMax={yMax} height={40} />
        ) : (
          <Box h="40px" />
        )}
      </Box>
      <Text fontSize="2xs" color="fg.subtle">
        last 15 min
      </Text>
    </VStack>
  );
}

function Field({
  label,
  value,
  hint,
  mono,
}: {
  label: string;
  value: string;
  hint?: string;
  mono?: boolean;
}) {
  return (
    <VStack
      align="start"
      gap="1"
      bg="surface.subtle"
      borderRadius="md"
      borderWidth="1px"
      borderColor="border.subtle"
      p="3"
    >
      <Text
        fontSize="xs"
        color="fg.muted"
        textTransform="uppercase"
        letterSpacing="0.06em"
      >
        {label}
      </Text>
      <Text
        fontSize="sm"
        fontFamily={mono ? "mono" : undefined}
        fontVariantNumeric={mono ? "tabular-nums" : undefined}
        fontWeight="medium"
        wordBreak="break-all"
      >
        {value}
      </Text>
      {hint ? (
        <Text fontSize="xs" color="fg.subtle">
          {hint}
        </Text>
      ) : null}
    </VStack>
  );
}
