import { Box } from "@chakra-ui/react";
import type { ReactNode } from "react";

import { Header } from "./Header";

export function AppLayout({ children }: { children: ReactNode }) {
  return (
    <Box minH="100vh" bg="surface.canvas">
      <Header />
      <Box as="main" maxW="1400px" mx="auto" px={{ base: "4", md: "6" }} py="6">
        {children}
      </Box>
    </Box>
  );
}
