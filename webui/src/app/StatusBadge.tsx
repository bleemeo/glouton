import { Box, HStack, Text } from "@chakra-ui/react";

export type Status = "ok" | "warn" | "crit" | "unknown";

const colors: Record<Status, { bg: string; ring: string; label: string }> = {
  ok: { bg: "kpi.ok.bg", ring: "kpi.ok.ring", label: "OK" },
  warn: { bg: "kpi.warn.bg", ring: "kpi.warn.ring", label: "Warning" },
  crit: { bg: "kpi.crit.bg", ring: "kpi.crit.ring", label: "Critical" },
  unknown: { bg: "kpi.neutral.bg", ring: "fg.subtle", label: "Unknown" },
};

export function StatusBadge({ status, label }: { status: Status; label?: string }) {
  const c = colors[status];

  return (
    <HStack
      gap="2"
      px="2.5"
      py="1"
      bg={c.bg}
      borderRadius="full"
      borderWidth="1px"
      borderColor={c.ring}
    >
      <Box w="2" h="2" borderRadius="full" bg={c.ring} />
      <Text fontSize="xs" fontWeight="medium" color="fg.default">
        {label ?? c.label}
      </Text>
    </HStack>
  );
}
