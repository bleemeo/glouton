import {
  Box,
  Heading,
  HStack,
  SimpleGrid,
  Spinner,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react";

import { useFetch } from "../api/hooks";
import type { AgentInformation, Fact, Service } from "../api/types";
import { StatusBadge, type Status } from "../app/StatusBadge";

const HOST_FACT_KEYS = [
  "fqdn",
  "hostname",
  "os_name",
  "os_version",
  "kernel_version",
  "architecture",
  "cpu_model_name",
  "cpu_cores",
  "memory",
  "primary_address",
  "primary_mac_address",
  "timezone",
  "virtual",
  "domain",
];

const RUNTIME_FACT_KEYS = [
  "agent_version",
  "glouton_version",
  "installation_format",
  "boot_time",
  "kernel",
  "uptime",
  "container",
  "container_runtime",
];

function pickFacts(facts: Fact[] | null, keys: string[]): Fact[] {
  if (!facts) return [];

  return keys
    .map((k) => facts.find((f) => f.name === k))
    .filter((f): f is Fact => f !== undefined && f.value !== "");
}

function statusFromCode(code: number | undefined): Status {
  if (code == null) return "unknown";
  if (code === 0) return "ok";
  if (code === 1) return "warn";
  if (code >= 2) return "crit";

  return "unknown";
}

function formatTimestamp(iso: string | undefined): string {
  if (!iso || iso === "0001-01-01T00:00:00Z") return "never";

  const d = new Date(iso);

  return d.toLocaleString();
}

export function Informations() {
  const facts = useFetch<Fact[]>("/data/facts", 60_000);
  const agent = useFetch<AgentInformation>("/data/agent-informations", 30_000);
  const services = useFetch<Service[]>("/data/services", 30_000);

  return (
    <VStack align="stretch" gap="6">
      <VStack align="start" gap="0">
        <Heading size="md">Informations</Heading>
        <Text fontSize="sm" color="fg.muted">
          Agent registration, host facts and discovered services.
        </Text>
      </VStack>

      <SimpleGrid columns={{ base: 1, lg: 3 }} gap="4">
        <FactsCard
          title="Bleemeo agent"
          loading={agent.loading}
          error={agent.error}
          rows={
            agent.data
              ? [
                  ["Status", agent.data.isConnected ? "Connected" : "Disconnected"],
                  ["Registered at", formatTimestamp(agent.data.registrationAt)],
                  ["Last report", formatTimestamp(agent.data.lastReport)],
                ]
              : []
          }
        />

        <FactsCard
          title="Host"
          loading={facts.loading}
          error={facts.error}
          rows={pickFacts(facts.data, HOST_FACT_KEYS).map((f) => [f.name, f.value])}
        />

        <FactsCard
          title="Runtime"
          loading={facts.loading}
          error={facts.error}
          rows={pickFacts(facts.data, RUNTIME_FACT_KEYS).map((f) => [f.name, f.value])}
        />
      </SimpleGrid>

      <VStack align="stretch" gap="2">
        <Heading size="sm" color="fg.muted" letterSpacing="0.06em" textTransform="uppercase">
          Discovered services
        </Heading>
        <Box
          bg="surface.panel"
          borderWidth="1px"
          borderColor="border.subtle"
          borderRadius="lg"
          overflow="hidden"
        >
          {services.error ? (
            <Box p="6" color="status.crit">
              Failed to load services: {services.error.message}
            </Box>
          ) : services.loading && (services.data?.length ?? 0) === 0 ? (
            <HStack p="6" gap="3">
              <Spinner size="sm" />
              <Text color="fg.muted">Loading…</Text>
            </HStack>
          ) : (services.data?.length ?? 0) === 0 ? (
            <Box p="6" color="fg.muted">
              No service discovered.
            </Box>
          ) : (
            <Table.Root size="sm" variant="line">
              <Table.Header bg="surface.subtle">
                <Table.Row>
                  <Table.ColumnHeader>Status</Table.ColumnHeader>
                  <Table.ColumnHeader>Name</Table.ColumnHeader>
                  <Table.ColumnHeader>Address</Table.ColumnHeader>
                  <Table.ColumnHeader>Listen</Table.ColumnHeader>
                  <Table.ColumnHeader>Description</Table.ColumnHeader>
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {services.data!.map((s) => (
                  <Table.Row key={`${s.name}-${s.containerId}-${s.ipAddress}`}>
                    <Table.Cell>
                      <StatusBadge status={statusFromCode(s.status)} />
                    </Table.Cell>
                    <Table.Cell fontWeight="medium">{s.name}</Table.Cell>
                    <Table.Cell fontFamily="mono" fontSize="sm" color="fg.muted">
                      {s.ipAddress || "—"}
                    </Table.Cell>
                    <Table.Cell fontFamily="mono" fontSize="sm" color="fg.muted">
                      {(s.listenAddresses ?? []).join(", ") || "—"}
                    </Table.Cell>
                    <Table.Cell color="fg.subtle" fontSize="xs">
                      {s.statusDescription ?? ""}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
          )}
        </Box>
      </VStack>
    </VStack>
  );
}

function FactsCard({
  title,
  rows,
  loading,
  error,
}: {
  title: string;
  rows: Array<[string, string]>;
  loading: boolean;
  error: Error | null;
}) {
  return (
    <Box
      bg="surface.panel"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="lg"
      p="4"
    >
      <Heading size="sm" mb="3">
        {title}
      </Heading>
      {error ? (
        <Text color="status.crit" fontSize="sm">
          {error.message}
        </Text>
      ) : loading && rows.length === 0 ? (
        <Spinner size="sm" />
      ) : rows.length === 0 ? (
        <Text color="fg.muted" fontSize="sm">
          No data.
        </Text>
      ) : (
        <VStack align="stretch" gap="2">
          {rows.map(([k, v]) => (
            <HStack key={k} justify="space-between" align="start" gap="3">
              <Text fontSize="xs" color="fg.muted" textTransform="uppercase" letterSpacing="0.04em">
                {k.replace(/_/g, " ")}
              </Text>
              <Text fontSize="sm" fontFamily="mono" textAlign="end" maxW="60%" wordBreak="break-word">
                {v}
              </Text>
            </HStack>
          ))}
        </VStack>
      )}
    </Box>
  );
}
