import { Heading, VStack } from "@chakra-ui/react";
import { useMemo, useState } from "react";

import { useFetch, useStoreInfo } from "../api/hooks";
import type { Fact } from "../api/types";
import { ChartGrid } from "./ChartGrid";
import { KPIRow } from "./KPIRow";
import { RangeSelector } from "./RangeSelector";
import { DEFAULT_RANGE_ID, RANGES, type Range } from "./ranges";

export function Dashboard() {
  const storeInfo = useStoreInfo();
  const facts = useFetch<Fact[]>("/data/facts", 60_000);

  const cores = useMemo(() => {
    const cpuCores = facts.data?.find((f) => f.name === "cpu_cores");
    if (cpuCores?.value) {
      const n = parseInt(cpuCores.value, 10);
      if (isFinite(n) && n > 0) return n;
    }
    return undefined;
  }, [facts.data]);

  const [range, setRange] = useState<Range>(
    () => RANGES.find((r) => r.id === DEFAULT_RANGE_ID) ?? RANGES[0],
  );

  return (
    <VStack align="stretch" gap="6">
      <KPIRow cores={cores} />

      <RangeSelector
        selectedId={range.id}
        onSelect={setRange}
        storeInfo={storeInfo.data}
      />

      <VStack align="stretch" gap="3">
        <Heading size="sm" color="fg.muted" letterSpacing="0.06em" textTransform="uppercase">
          System metrics
        </Heading>
        <ChartGrid range={range} />
      </VStack>
    </VStack>
  );
}
