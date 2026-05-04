import { Text } from "@chakra-ui/react";
import { useEffect, useState } from "react";

const formatter = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});

export function LiveClock() {
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), 1_000);

    return () => window.clearInterval(t);
  }, []);

  return (
    <Text
      fontFamily="mono"
      fontSize="sm"
      color="fg.muted"
      letterSpacing="0.02em"
      fontVariantNumeric="tabular-nums"
    >
      {formatter.format(now)}
    </Text>
  );
}
