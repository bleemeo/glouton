import {
  Code,
  Drawer,
  HStack,
  IconButton,
  Portal,
  SimpleGrid,
  Text,
  VStack,
} from "@chakra-ui/react";
import { LuX } from "react-icons/lu";

import type { Container } from "../api/types";
import { StatusBadge, type Status } from "../app/StatusBadge";
import { formatBytes } from "../dashboard/format";

type Props = {
  container: Container | null;
  requestedId: string | null;
  onClose: () => void;
};

function stateToStatus(state: string): Status {
  const s = state.toLowerCase();

  if (s.includes("running") || s === "up") return "ok";
  if (s.includes("paused") || s.includes("restarting")) return "warn";
  if (s.includes("exited") || s.includes("dead") || s.includes("stopped")) return "crit";

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
          <Drawer.Content bg="surface.panel" borderLeftWidth="1px" borderColor="border.subtle">
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

function ContainerDetails({ container: c, onClose }: { container: Container; onClose: () => void }) {
  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle" pb="3">
        <HStack justify="space-between" align="start">
          <VStack align="start" gap="2">
            <StatusBadge status={stateToStatus(c.state)} label={c.state} />
            <Drawer.Title fontSize="lg" fontWeight="semibold" wordBreak="break-all">
              {c.name}
            </Drawer.Title>
            <Text fontSize="xs" color="fg.subtle" fontFamily="mono">
              {c.id.slice(0, 24)}
            </Text>
          </VStack>
          <IconButton aria-label="Close" size="sm" variant="ghost" onClick={onClose}>
            <LuX />
          </IconButton>
        </HStack>
      </Drawer.Header>

      <Drawer.Body>
        <VStack align="stretch" gap="5" pt="2">
          <SimpleGrid columns={2} gap="3">
            <Field label="CPU" value={`${c.cpuUsedPerc.toFixed(1)} %`} mono />
            <Field label="Memory" value={`${c.memUsedPerc.toFixed(1)} %`} mono />
            <Field label="↓ Network" value={`${formatBytes(c.netBitsRecv / 8)}/s`} mono />
            <Field label="↑ Network" value={`${formatBytes(c.netBitsSent / 8)}/s`} mono />
            <Field label="Read" value={`${formatBytes(c.ioReadBytes)}/s`} mono />
            <Field label="Write" value={`${formatBytes(c.ioWriteBytes)}/s`} mono />
          </SimpleGrid>

          <Section label="Image">
            <Code variant="surface" fontFamily="mono" fontSize="xs" wordBreak="break-all" p="2">
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

function RemovedNotice({ id, onClose }: { id: string | null; onClose: () => void }) {
  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle">
        <HStack justify="space-between">
          <Drawer.Title>Container removed</Drawer.Title>
          <IconButton aria-label="Close" size="sm" variant="ghost" onClick={onClose}>
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

function Section({ label, children }: { label: string; children: React.ReactNode }) {
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
