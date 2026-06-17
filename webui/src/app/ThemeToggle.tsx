import { IconButton } from "@chakra-ui/react";
import { useTheme } from "next-themes";
import { useEffect, useState } from "react";
import { LuMonitor, LuMoon, LuSun } from "react-icons/lu";

const order = ["system", "light", "dark"] as const;
type Mode = (typeof order)[number];

export function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);

  // next-themes can only resolve the active value after hydration.
  useEffect(() => setMounted(true), []);

  if (!mounted) {
    return null;
  }

  const current = (theme ?? "system") as Mode;
  const next = order[(order.indexOf(current) + 1) % order.length];

  const Icon =
    current === "dark" ? LuMoon : current === "light" ? LuSun : LuMonitor;

  return (
    <IconButton
      aria-label={`Theme: ${current}. Click to switch to ${next}.`}
      title={`Theme: ${current}`}
      size="sm"
      variant="ghost"
      onClick={() => setTheme(next)}
    >
      <Icon />
    </IconButton>
  );
}
