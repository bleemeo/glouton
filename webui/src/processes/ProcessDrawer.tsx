import {
  Box,
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

import type { Process } from "../api/types";
import { formatBytes, formatNumber } from "../dashboard/format";

const STATUS_META: Record<string, { label: string; color: string }> = {
  R: { label: "Running", color: "#10B981" },
  S: { label: "Sleeping", color: "#9CA3AF" },
  D: { label: "Disk wait", color: "#F59E0B" },
  T: { label: "Stopped", color: "#F59E0B" },
  Z: { label: "Zombie", color: "#EF4444" },
  I: { label: "Idle", color: "#6B7280" },
};

function statusMeta(s: string) {
  if (!s) return { label: "—", color: "#6B7280" };
  return STATUS_META[s[0].toUpperCase()] ?? { label: s, color: "#9CA3AF" };
}

type Props = {
  process: Process | null;
  // requestedPid is set when the user clicked a row but the process is no
  // longer in the latest poll (it exited). Used to show a friendly notice.
  requestedPid: number | null;
  onClose: () => void;
};

export function ProcessDrawer({ process, requestedPid, onClose }: Props) {
  const open = process !== null || requestedPid !== null;

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
            {process ? (
              <ProcessDetails process={process} onClose={onClose} />
            ) : (
              <ExitedNotice pid={requestedPid} onClose={onClose} />
            )}
          </Drawer.Content>
        </Drawer.Positioner>
      </Portal>
    </Drawer.Root>
  );
}

function ProcessDetails({ process: p, onClose }: { process: Process; onClose: () => void }) {
  const status = statusMeta(p.status);
  const created = p.create_time ? new Date(p.create_time) : null;

  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle" pb="3">
        <HStack justify="space-between" align="start">
          <VStack align="start" gap="1">
            <HStack gap="2" align="center">
              <Box w="2.5" h="2.5" borderRadius="full" bg={status.color} />
              <Text fontSize="xs" color="fg.muted">
                {status.label} · PID {p.pid}
              </Text>
            </HStack>
            <Drawer.Title fontSize="lg" fontWeight="semibold">
              {p.name}
            </Drawer.Title>
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
          <SimpleGrid columns={2} gap="3">
            <Field label="CPU" value={`${p.cpu_percent.toFixed(2)} %`} mono />
            <Field label="Memory (RSS)" value={formatBytes(p.memory_rss)} mono />
            <Field label="CPU time" value={`${formatNumber(p.cpu_time, 2)} s`} mono />
            <Field label="User" value={p.username || "—"} />
            <Field label="PPID" value={p.ppid > 0 ? String(p.ppid) : "—"} mono />
            <Field
              label="Started"
              value={created ? created.toLocaleString() : "—"}
            />
          </SimpleGrid>

          {p.container_id ? (
            <Section label="Container">
              <Code variant="surface" fontFamily="mono" fontSize="xs">
                {p.container_id}
              </Code>
            </Section>
          ) : null}

          {p.executable ? (
            <Section label="Executable">
              <Code
                variant="surface"
                fontFamily="mono"
                fontSize="xs"
                wordBreak="break-all"
                whiteSpace="pre-wrap"
                p="2"
                w="full"
              >
                {p.executable}
              </Code>
            </Section>
          ) : null}

          <Section label="Command line">
            <Code
              variant="surface"
              fontFamily="mono"
              fontSize="xs"
              wordBreak="break-all"
              whiteSpace="pre-wrap"
              p="3"
              w="full"
            >
              {p.cmdline || p.executable || "—"}
            </Code>
          </Section>
        </VStack>
      </Drawer.Body>
    </>
  );
}

function ExitedNotice({ pid, onClose }: { pid: number | null; onClose: () => void }) {
  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle">
        <HStack justify="space-between">
          <Drawer.Title>Process exited</Drawer.Title>
          <IconButton aria-label="Close" size="sm" variant="ghost" onClick={onClose}>
            <LuX />
          </IconButton>
        </HStack>
      </Drawer.Header>
      <Drawer.Body>
        <Text color="fg.muted" mt="4">
          {pid != null
            ? `PID ${pid} is no longer in the process list.`
            : "The selected process is no longer available."}
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
  mono,
}: {
  label: string;
  value: string;
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
      >
        {value}
      </Text>
    </VStack>
  );
}
