import { SimpleGrid } from "@chakra-ui/react";

import { usePromQLRange } from "../api/hooks";
import { formatBitsPerSec, formatNumber, formatPercent } from "./format";
import { KPICard } from "./KPICard";
import { lastValue } from "./promql";
import { DEFAULT_THRESHOLDS, statusFromValue } from "./status";

const SHORT_RANGE = 60; // seconds — the "now" snapshot the cards summarise
const SHORT_STEP = 10;

export function KPIRow({ cores }: { cores: number | undefined }) {
  const cpu = usePromQLRange("cpu_used", SHORT_RANGE, SHORT_STEP);
  const mem = usePromQLRange("mem_used_perc", SHORT_RANGE, SHORT_STEP);
  const swap = usePromQLRange("swap_used_perc", SHORT_RANGE, SHORT_STEP);
  const load = usePromQLRange("system_load1", SHORT_RANGE, SHORT_STEP);
  const netIn = usePromQLRange("sum(net_bits_recv)", SHORT_RANGE, SHORT_STEP);
  const netOut = usePromQLRange("sum(net_bits_sent)", SHORT_RANGE, SHORT_STEP);

  const cpuV = lastValue(cpu.data);
  const memV = lastValue(mem.data);
  const swapV = lastValue(swap.data);
  const loadV = lastValue(load.data);
  const netInV = lastValue(netIn.data);
  const netOutV = lastValue(netOut.data);

  // Normalise load by core count so the warn/crit thresholds make sense
  // across different machines.
  const normalisedLoad = loadV != null && cores ? loadV / cores : loadV;
  const loadThresholds =
    cores != null
      ? { warn: 0.8, crit: 1.5 }
      : DEFAULT_THRESHOLDS.load;

  return (
    <SimpleGrid columns={{ base: 2, md: 3, xl: 6 }} gap="3">
      <KPICard
        label="CPU"
        value={formatPercent(cpuV)}
        fillRatio={cpuV == null ? undefined : cpuV / 100}
        status={statusFromValue(cpuV, DEFAULT_THRESHOLDS.cpu)}
        hint={cores ? `${cores} cores` : undefined}
      />
      <KPICard
        label="RAM"
        value={formatPercent(memV)}
        fillRatio={memV == null ? undefined : memV / 100}
        status={statusFromValue(memV, DEFAULT_THRESHOLDS.ram)}
      />
      <KPICard
        label="Swap"
        value={formatPercent(swapV)}
        fillRatio={swapV == null ? undefined : swapV / 100}
        status={statusFromValue(swapV, DEFAULT_THRESHOLDS.swap)}
      />
      <KPICard
        label="Load avg (1m)"
        value={formatNumber(loadV, 2)}
        fillRatio={
          normalisedLoad == null ? undefined : Math.min(1, normalisedLoad / 1.5)
        }
        status={statusFromValue(normalisedLoad, loadThresholds)}
        hint={cores ? `per core: ${formatNumber(normalisedLoad ?? 0, 2)}` : undefined}
      />
      <KPICard
        label="↓ Network"
        value={formatBitsPerSec(netInV)}
        status="unknown"
      />
      <KPICard
        label="↑ Network"
        value={formatBitsPerSec(netOutV)}
        status="unknown"
      />
    </SimpleGrid>
  );
}
