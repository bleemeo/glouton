import {
  Box,
  chakra,
  Heading,
  HStack,
  Input,
  SimpleGrid,
  Spinner,
  Text,
  VStack,
} from "@chakra-ui/react";
import { useCallback, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { useFetch } from "../api/hooks";
import type { Monitor, MonitorsResponse } from "../api/types";
import { CertExpiryBanner } from "./CertExpiryBanner";
import { MonitorCard } from "./MonitorCard";
import { MonitorDrawer } from "./MonitorDrawer";
import { MonitorsKPIRow } from "./MonitorsKPIRow";
import { useMonitorStates } from "./useMonitorStates";

export function Monitors() {
  // The drawer state is driven by the URL (/monitors/:name): bookmarks
  // and shared links go straight into the right monitor. Closing the
  // drawer navigates back to /monitors and clears the param.
  const { name: selectedName } = useParams();
  const navigate = useNavigate();

  const openMonitor = useCallback(
    (m: Monitor) => navigate(`/monitors/${encodeURIComponent(m.name)}`),
    [navigate],
  );
  const closeDrawer = useCallback(() => navigate("/monitors"), [navigate]);

  const [search, setSearch] = useState("");
  const [onlyDown, setOnlyDown] = useState(false);

  const res = useFetch<MonitorsResponse>("/data/monitors", 60_000);
  const monitors: Monitor[] = useMemo(
    () => res.data?.monitors ?? [],
    [res.data],
  );

  // Batch all metric queries at the page level so each card receives
  // its slice via props instead of firing its own round-trips.
  const { states, loading } = useMonitorStates(monitors);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return states.filter((s) => {
      if (onlyDown && s.status === "ok") return false;
      if (!q) return true;
      return (
        s.monitor.name.toLowerCase().includes(q) ||
        s.monitor.url.toLowerCase().includes(q)
      );
    });
  }, [states, search, onlyDown]);

  const selected = useMemo(
    () =>
      selectedName
        ? (monitors.find((m) => m.name === selectedName) ?? null)
        : null,
    [monitors, selectedName],
  );

  const downCount = useMemo(
    () => states.filter((s) => s.status === "down").length,
    [states],
  );

  return (
    <VStack align="stretch" gap="4">
      <HStack justify="space-between" wrap="wrap" gap="3">
        <VStack align="start" gap="0">
          <Heading size="md">Uptime Monitoring</Heading>
          <Text fontSize="sm" color="fg.muted">
            {monitors.length === 0
              ? "No monitors configured."
              : `${monitors.length} monitor${monitors.length === 1 ? "" : "s"}`}
          </Text>
        </VStack>

        {monitors.length > 0 ? (
          <HStack gap="2">
            <Input
              placeholder="Filter by name or URL…"
              size="sm"
              w={{ base: "full", md: "260px" }}
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
            />
            <chakra.button
              type="button"
              onClick={() => setOnlyDown((v) => !v)}
              disabled={downCount === 0 && !onlyDown}
              px="3"
              py="1.5"
              fontSize="sm"
              fontWeight="medium"
              borderRadius="md"
              borderWidth="1px"
              borderColor={onlyDown ? "#EF4444" : "border.default"}
              bg={onlyDown ? "rgba(239, 68, 68, 0.08)" : "surface.panel"}
              color={onlyDown ? "#EF4444" : "fg.default"}
              cursor={downCount === 0 && !onlyDown ? "not-allowed" : "pointer"}
              opacity={downCount === 0 && !onlyDown ? 0.5 : 1}
              _hover={
                downCount === 0 && !onlyDown ? {} : { bg: "surface.subtle" }
              }
              title={
                downCount === 0
                  ? "All monitors are up"
                  : onlyDown
                    ? "Show all monitors"
                    : "Show only monitors currently down"
              }
            >
              {onlyDown ? `Showing down only (${downCount})` : "Show only down"}
            </chakra.button>
          </HStack>
        ) : null}
      </HStack>

      {res.error ? (
        <Box p="6" color="status.crit">
          Failed to load monitors: {res.error.message}
        </Box>
      ) : res.loading && monitors.length === 0 ? (
        <HStack p="6" gap="3">
          <Spinner size="sm" />
          <Text color="fg.muted">Loading…</Text>
        </HStack>
      ) : monitors.length === 0 ? (
        <Box
          bg="surface.panel"
          borderWidth="1px"
          borderColor="border.subtle"
          borderRadius="lg"
          p="6"
          color="fg.muted"
        >
          <Text fontSize="sm">
            Declare HTTP, TCP, ICMP, DNS or SSL targets under{" "}
            <Text as="code" fontFamily="mono">
              blackbox.targets
            </Text>{" "}
            in your config to see them here.
          </Text>
        </Box>
      ) : (
        <>
          <MonitorsKPIRow monitors={monitors} />
          <CertExpiryBanner states={states} onMonitorClick={openMonitor} />
          {filtered.length === 0 ? (
            <Box
              bg="surface.panel"
              borderWidth="1px"
              borderColor="border.subtle"
              borderRadius="lg"
              p="6"
              color="fg.muted"
            >
              <Text fontSize="sm">No monitor matches the current filter.</Text>
            </Box>
          ) : (
            <SimpleGrid columns={{ base: 1, md: 2, xl: 3 }} gap="3">
              {filtered.map((state) => (
                <MonitorCard
                  key={state.monitor.url}
                  state={state}
                  loading={loading}
                  onClick={() => openMonitor(state.monitor)}
                />
              ))}
            </SimpleGrid>
          )}
        </>
      )}

      <MonitorDrawer monitor={selected} onClose={closeDrawer} />
    </VStack>
  );
}
