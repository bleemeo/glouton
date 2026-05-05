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

import type { Service } from "../api/types";
import { StatusBadge, type Status } from "../app/StatusBadge";

const STATUS_LABEL: Record<number, string> = {
  0: "OK",
  1: "Warning",
  2: "Critical",
  3: "Unknown",
};

function statusFromCode(code: number | undefined): Status {
  if (code == null) return "unknown";
  if (code === 0) return "ok";
  if (code === 1) return "warn";
  if (code >= 2) return "crit";
  return "unknown";
}

function statusLabel(code: number | undefined): string {
  if (code == null) return "Unknown";
  return STATUS_LABEL[code] ?? STATUS_LABEL[Math.min(2, code)] ?? "Unknown";
}

type Props = {
  service: Service | null;
  onClose: () => void;
};

export function ServiceDrawer({ service, onClose }: Props) {
  return (
    <Drawer.Root
      open={service !== null}
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
            {service ? <Details service={service} onClose={onClose} /> : null}
          </Drawer.Content>
        </Drawer.Positioner>
      </Portal>
    </Drawer.Root>
  );
}

function Details({ service: s, onClose }: { service: Service; onClose: () => void }) {
  const status = statusFromCode(s.status);

  return (
    <>
      <Drawer.Header borderBottomWidth="1px" borderColor="border.subtle" pb="3">
        <HStack justify="space-between" align="start">
          <VStack align="start" gap="2">
            <StatusBadge status={status} label={statusLabel(s.status)} />
            <Drawer.Title fontSize="lg" fontWeight="semibold" wordBreak="break-all">
              {s.name}
            </Drawer.Title>
          </VStack>
          <IconButton aria-label="Close" size="sm" variant="ghost" onClick={onClose}>
            <LuX />
          </IconButton>
        </HStack>
      </Drawer.Header>

      <Drawer.Body>
        <VStack align="stretch" gap="5" pt="2">
          <SimpleGrid columns={{ base: 1, sm: 2 }} gap="3">
            <Field label="Active" value={s.active ? "Yes" : "No"} />
            <Field
              label="IP address"
              value={s.ipAddress || "—"}
              mono={Boolean(s.ipAddress)}
            />
          </SimpleGrid>

          {s.listenAddresses && s.listenAddresses.length > 0 ? (
            <Section label={`Listening (${s.listenAddresses.length})`}>
              <VStack align="stretch" gap="1">
                {s.listenAddresses.map((addr) => (
                  <Code
                    key={addr}
                    variant="surface"
                    fontFamily="mono"
                    fontSize="xs"
                    p="2"
                  >
                    {addr}
                  </Code>
                ))}
              </VStack>
            </Section>
          ) : null}

          {s.exePath ? (
            <Section label="Executable">
              <Code
                variant="surface"
                fontFamily="mono"
                fontSize="xs"
                wordBreak="break-all"
                whiteSpace="pre-wrap"
                p="2"
              >
                {s.exePath}
              </Code>
            </Section>
          ) : null}

          {s.containerId ? (
            <Section label="Container">
              <Code variant="surface" fontFamily="mono" fontSize="xs" p="2">
                {s.containerId}
              </Code>
            </Section>
          ) : null}

          {s.statusDescription ? (
            <Section label="Status detail">
              <Text
                fontSize="sm"
                color="fg.default"
                bg="surface.subtle"
                borderWidth="1px"
                borderColor="border.subtle"
                borderRadius="md"
                p="3"
              >
                {s.statusDescription}
              </Text>
            </Section>
          ) : null}
        </VStack>
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
        wordBreak="break-all"
      >
        {value}
      </Text>
    </VStack>
  );
}
