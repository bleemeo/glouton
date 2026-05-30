import { Box, chakra, HStack, Text, VStack } from "@chakra-ui/react";
import { useState } from "react";

import { useFetch } from "../api/hooks";
import type { Service } from "../api/types";
import { ServiceDrawer } from "./ServiceDrawer";

function serviceKey(s: Service): string {
  return `${s.name}-${s.containerId}-${s.ipAddress}`;
}

const STATUS_COLOR: Record<number, string> = {
  0: "#10B981", // ok
  1: "#F59E0B", // warning
  2: "#EF4444", // critical
  3: "#9CA3AF", // unknown
};

function statusColor(code: number | undefined): string {
  if (code == null) return "#9CA3AF";
  return STATUS_COLOR[code] ?? STATUS_COLOR[Math.min(2, code)] ?? "#9CA3AF";
}

export function ServicesRow() {
  const res = useFetch<Service[]>("/data/services", 30_000);
  const services = res.data ?? [];
  const [selectedKey, setSelectedKey] = useState<string | null>(null);

  // Hide the section entirely when no service is detected — most
  // common case on dev machines and we'd rather show nothing than
  // an empty placeholder.
  if (!res.loading && services.length === 0) {
    return null;
  }

  // Sort by status descending (worst first) then by name, so anything
  // failing surfaces immediately at the start of the row.
  const sorted = [...services].sort((a, b) => {
    if (b.status !== a.status) return (b.status ?? 0) - (a.status ?? 0);
    return a.name.localeCompare(b.name);
  });

  const selected = selectedKey
    ? (sorted.find((s) => serviceKey(s) === selectedKey) ?? null)
    : null;

  return (
    <VStack align="stretch" gap="2">
      <Text
        fontSize="xs"
        fontWeight="semibold"
        color="fg.muted"
        letterSpacing="0.08em"
        textTransform="uppercase"
      >
        Services
      </Text>
      <HStack gap="1.5" wrap="wrap">
        {sorted.map((s) => (
          <ServiceChip
            key={serviceKey(s)}
            service={s}
            onClick={() => setSelectedKey(serviceKey(s))}
          />
        ))}
      </HStack>

      <ServiceDrawer service={selected} onClose={() => setSelectedKey(null)} />
    </VStack>
  );
}

function ServiceChip({
  service,
  onClick,
}: {
  service: Service;
  onClick: () => void;
}) {
  const color = statusColor(service.status);
  const detail =
    service.statusDescription ||
    `${service.name}${service.ipAddress ? " — " + service.ipAddress : ""}`;

  return (
    <chakra.button
      type="button"
      onClick={onClick}
      px="2.5"
      py="1"
      bg="surface.panel"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="full"
      cursor="pointer"
      transition="all 100ms ease"
      _hover={{ bg: "surface.subtle", borderColor: "border.default" }}
      title={detail}
      display="inline-flex"
      alignItems="center"
      gap="2"
    >
      <Box w="2" h="2" borderRadius="full" bg={color} />
      <Text fontSize="xs" fontFamily="mono" color="fg.default">
        {service.name}
      </Text>
    </chakra.button>
  );
}
