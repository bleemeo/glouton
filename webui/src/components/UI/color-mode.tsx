/* eslint-disable @typescript-eslint/no-empty-object-type */
"use client";

import type { IconButtonProps } from "@chakra-ui/react";
import { ClientOnly, IconButton, Skeleton } from "@chakra-ui/react";
import { ThemeProvider, useTheme } from "next-themes";
import type { ThemeProviderProps } from "next-themes";
import * as React from "react";
import { LuMoon, LuSun } from "react-icons/lu";
import { WiMoonAltFirstQuarter } from "react-icons/wi";

export interface ColorModeProviderProps extends ThemeProviderProps {}

export function ColorModeProvider(props: ColorModeProviderProps) {
  return (
    <ThemeProvider attribute="class" disableTransitionOnChange {...props} />
  );
}

export type ColorMode = "light" | "dark";

export type ColorModeOrSystem = ColorMode | "system";

export interface UseColorModeReturn {
  colorMode: ColorMode;
  setColorMode: (colorMode: ColorMode) => void;
  toggleColorMode: () => void;
  theme: ColorModeOrSystem;
}

export function useColorMode(): UseColorModeReturn {
  const { resolvedTheme, setTheme, forcedTheme, theme } = useTheme();
  const colorMode = forcedTheme || resolvedTheme;
  const toggleColorMode = React.useCallback(() => {
    const cycle: Array<ColorModeOrSystem> = ["system", "light", "dark"];
    const current = (theme ?? "system") as ColorModeOrSystem;
    const next = cycle[(cycle.indexOf(current) + 1) % cycle.length];
    setTheme(next);
  }, [theme, setTheme]);
  return {
    colorMode: colorMode as ColorMode,
    setColorMode: setTheme,
    toggleColorMode,
    theme: theme as ColorModeOrSystem,
  };
}

export function ColorModeIcon() {
  const { theme } = useColorMode();
  return theme === "dark" ? (
    <LuMoon />
  ) : theme === "light" ? (
    <LuSun />
  ) : (
    <WiMoonAltFirstQuarter />
  );
}

interface ColorModeButtonProps extends Omit<IconButtonProps, "aria-label"> {}

export const ColorModeButton = React.forwardRef<
  HTMLButtonElement,
  ColorModeButtonProps
>(function ColorModeButton(props, ref) {
  const { toggleColorMode } = useColorMode();
  return (
    <ClientOnly fallback={<Skeleton boxSize="8" />}>
      <IconButton
        onClick={toggleColorMode}
        variant="ghost"
        aria-label="Toggle color mode"
        size="sm"
        ref={ref}
        {...props}
        css={{
          _icon: {
            width: "5",
            height: "5",
          },
        }}
      >
        <ColorModeIcon />
      </IconButton>
    </ClientOnly>
  );
});
