import { Box, chakra, HStack, Text, VStack } from "@chakra-ui/react";

import type { StoreInfo } from "../api/types";
import { RANGES, isRangeAvailable, type Range } from "./ranges";

type Props = {
  selectedId: string;
  onSelect: (range: Range) => void;
  storeInfo: StoreInfo | null;
};

export function RangeSelector({ selectedId, onSelect, storeInfo }: Props) {
  const oldest = storeInfo?.oldest_point_ms;

  return (
    <VStack align="start" gap="1.5">
      <HStack gap="0.5" wrap="wrap">
        {RANGES.map((r) => {
          const available = isRangeAvailable(r, oldest);
          const active = r.id === selectedId;

          return (
            <chakra.button
              key={r.id}
              type="button"
              disabled={!available}
              onClick={() => available && onSelect(r)}
              px="3"
              py="1.5"
              fontSize="sm"
              fontFamily="mono"
              fontWeight={active ? "semibold" : "medium"}
              color={
                active
                  ? "fg.default"
                  : available
                    ? "fg.muted"
                    : "fg.subtle"
              }
              bg={active ? "surface.subtle" : "transparent"}
              borderRadius="md"
              borderWidth="1px"
              borderColor={active ? "border.default" : "transparent"}
              cursor={available ? "pointer" : "not-allowed"}
              opacity={available ? 1 : 0.45}
              position="relative"
              transition="all 120ms ease"
              _hover={available ? { bg: "surface.subtle", color: "fg.default" } : {}}
              title={
                available
                  ? `Last ${r.label}`
                  : `Not available — local history is only ${formatDuration(
                      oldest ? (Date.now() - oldest) / 1000 : 0,
                    )}. Enable agent.local_store to see longer ranges.`
              }
            >
              {r.label}
              {active ? (
                <Box
                  position="absolute"
                  left="2"
                  right="2"
                  bottom="-3px"
                  h="2px"
                  borderRadius="full"
                  bgGradient="gradients.accent"
                />
              ) : null}
            </chakra.button>
          );
        })}
      </HStack>
      <Text fontSize="xs" color="fg.subtle" fontFamily="mono">
        {storeInfo?.persistent
          ? `Local TSDB enabled — ${formatDuration(storeInfo.retention_seconds)} retention`
          : "Local TSDB disabled — only the last 3 minutes are kept in memory"}
      </Text>
    </VStack>
  );
}

function formatDuration(seconds: number): string {
  if (seconds <= 0) return "0s";

  const days = Math.floor(seconds / 86_400);
  if (days >= 1) return `${days}d`;

  const hours = Math.floor(seconds / 3_600);
  if (hours >= 1) return `${hours}h`;

  const minutes = Math.floor(seconds / 60);
  if (minutes >= 1) return `${minutes}m`;

  return `${Math.floor(seconds)}s`;
}
