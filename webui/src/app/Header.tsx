import { Box, chakra, HStack, Text } from "@chakra-ui/react";
import { NavLink } from "react-router-dom";

import { useFetch } from "../api/hooks";
import type { AgentInformation, Fact } from "../api/types";
import { LiveClock } from "./LiveClock";
import { LiveIndicator } from "./LiveIndicator";
import { StatusBadge, type Status } from "./StatusBadge";
import { ThemeToggle } from "./ThemeToggle";

const NAV = [
  { to: "/dashboard", label: "Dashboard" },
  { to: "/containers", label: "Containers" },
  { to: "/processes", label: "Processes" },
  { to: "/informations", label: "Informations" },
] as const;

function HostName() {
  const facts = useFetch<Fact[]>("/data/facts", 60_000);

  if (!facts.data) {
    return null;
  }

  const fqdn = facts.data.find((f) => f.name === "fqdn")?.value;
  const hostname = facts.data.find((f) => f.name === "hostname")?.value;
  const display = fqdn ?? hostname;

  if (!display) {
    return null;
  }

  return (
    <Text fontFamily="mono" fontSize="sm" color="fg.muted" maxW="40ch" truncate>
      {display}
    </Text>
  );
}

function ConnectivityStatus() {
  const info = useFetch<AgentInformation>("/data/agent-informations", 30_000);

  let status: Status = "unknown";
  let label = "Local mode";

  if (info.data) {
    if (info.data.isConnected) {
      status = "ok";
      label = "Connected to Bleemeo";
    } else if (info.data.registrationAt && info.data.registrationAt !== "0001-01-01T00:00:00Z") {
      status = "warn";
      label = "Bleemeo disconnected";
    } else {
      status = "ok";
      label = "Local mode";
    }
  }

  return <StatusBadge status={status} label={label} />;
}

export function Header() {
  return (
    <Box
      as="header"
      position="sticky"
      top={0}
      zIndex="docked"
      bg="surface.panel"
      borderBottomWidth="1px"
      borderColor="border.subtle"
      backdropFilter="saturate(180%) blur(8px)"
    >
      <HStack px="6" py="3" gap="6">
        <HStack gap="3">
          <chakra.img
            src="/static/img/logo_glouton.svg"
            alt="Glouton"
            h="8"
            w="auto"
          />
          <Text fontWeight="bold" fontSize="md" letterSpacing="0.04em">
            GLOUTON
          </Text>
        </HStack>

        <HostName />

        <HStack as="nav" gap="1" ms="4">
          {NAV.map((item) => (
            <NavLink key={item.to} to={item.to} style={{ textDecoration: "none" }}>
              {({ isActive }) => (
                <Box
                  px="3"
                  py="1.5"
                  fontSize="sm"
                  fontWeight={isActive ? "semibold" : "medium"}
                  color={isActive ? "fg.default" : "fg.muted"}
                  borderRadius="md"
                  position="relative"
                  _hover={{ bg: "surface.subtle", color: "fg.default" }}
                  transition="background 120ms ease, color 120ms ease"
                >
                  {item.label}
                  {isActive ? (
                    <Box
                      position="absolute"
                      left="3"
                      right="3"
                      bottom="-1"
                      h="2px"
                      borderRadius="full"
                      bgGradient="gradients.accent"
                    />
                  ) : null}
                </Box>
              )}
            </NavLink>
          ))}
        </HStack>

        <Box flex="1" />

        <ConnectivityStatus />
        <LiveIndicator />
        <ThemeToggle />
        <LiveClock />
      </HStack>
    </Box>
  );
}
