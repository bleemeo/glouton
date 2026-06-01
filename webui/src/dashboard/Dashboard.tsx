import { Heading, VStack } from "@chakra-ui/react";
import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

import { useFetch, useStoreInfo } from "../api/hooks";
import type { Fact } from "../api/types";
import { NetworkAndIOChartGrid, SystemChartGrid } from "./ChartGrid";
import { KPIRow } from "./KPIRow";
import { MonitorsRow } from "./MonitorsRow";
import { RangeSelector } from "./RangeSelector";
import { ServicesRow } from "./ServicesRow";
import { DEFAULT_RANGE_ID, RANGES, type Range } from "./ranges";

const RANGE_PARAM = "range";

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

  // Range lives in the URL (?range=1h) so refresh keeps it and the
  // current view can be shared by copying the address bar. We accept
  // any known range id; unknown values fall back to the default.
  const [searchParams, setSearchParams] = useSearchParams();
  const range = useMemo<Range>(() => {
    const id = searchParams.get(RANGE_PARAM);
    return (
      RANGES.find((r) => r.id === id) ??
      RANGES.find((r) => r.id === DEFAULT_RANGE_ID) ??
      RANGES[0]
    );
  }, [searchParams]);

  const setRange = useCallback(
    (next: Range) => {
      setSearchParams(
        (prev) => {
          const params = new URLSearchParams(prev);
          if (next.id === DEFAULT_RANGE_ID) {
            // Keep the URL clean when the default is selected.
            params.delete(RANGE_PARAM);
          } else {
            params.set(RANGE_PARAM, next.id);
          }
          return params;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  return (
    <VStack align="stretch" gap="6">
      <KPIRow cores={cores} />

      <ServicesRow />

      <MonitorsRow />

      <RangeSelector
        selectedId={range.id}
        onSelect={setRange}
        storeInfo={storeInfo.data}
      />

      <VStack align="stretch" gap="3">
        <Heading
          size="sm"
          color="fg.muted"
          letterSpacing="0.06em"
          textTransform="uppercase"
        >
          System metrics
        </Heading>
        <SystemChartGrid range={range} />
      </VStack>

      <VStack align="stretch" gap="3">
        <Heading
          size="sm"
          color="fg.muted"
          letterSpacing="0.06em"
          textTransform="uppercase"
        >
          Network &amp; I/O
        </Heading>
        <NetworkAndIOChartGrid range={range} />
      </VStack>
    </VStack>
  );
}
