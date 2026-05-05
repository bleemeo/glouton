import { Box, HStack, Text } from "@chakra-ui/react";
import { useEffect, useState, useSyncExternalStore } from "react";

import { getLastFetchAt, subscribeFreshness } from "../api/freshness";

const STALE_AFTER_MS = 30_000;

export function LiveIndicator() {
  const lastFetchAt = useSyncExternalStore(
    subscribeFreshness,
    getLastFetchAt,
    () => 0,
  );

  // Re-render every second so the "X ago" label keeps ticking even
  // when no new fetch lands.
  const [, force] = useState(0);
  useEffect(() => {
    const t = window.setInterval(() => force((x) => x + 1), 1_000);
    return () => window.clearInterval(t);
  }, []);

  if (lastFetchAt === 0) {
    return (
      <HStack gap="2">
        <Box w="2" h="2" borderRadius="full" bg="fg.subtle" />
        <Text fontSize="xs" color="fg.muted" fontFamily="mono">
          waiting…
        </Text>
      </HStack>
    );
  }

  const ageMs = Date.now() - lastFetchAt;
  const stale = ageMs > STALE_AFTER_MS;

  return (
    <HStack gap="2" title={new Date(lastFetchAt).toLocaleTimeString()}>
      <Box
        w="2"
        h="2"
        borderRadius="full"
        bg={stale ? "status.warn" : "status.ok"}
        css={
          stale
            ? undefined
            : {
                animation: "live-pulse 2s ease-in-out infinite",
                "@keyframes live-pulse": {
                  "0%, 100%": { opacity: 1 },
                  "50%": { opacity: 0.35 },
                },
              }
        }
      />
      <Text fontSize="xs" color="fg.muted" fontFamily="mono">
        {formatAge(ageMs)}
      </Text>
    </HStack>
  );
}

function formatAge(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 5) return "live";
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  return `${h}h ago`;
}
