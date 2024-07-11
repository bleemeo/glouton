import React, { FC, useEffect, useRef } from "react";

import WidgetDashboardItem from "../UI/WidgetDashboardItem";
import MetricGaugeItem from "../Metric/MetricGaugeItem";

import {
  GaugeBar,
  gaugesBarBLEEMEO,
  gaugesBarPrometheusLinux,
  gaugesBarPrometheusWindows,
  NumberMetric,
  numberMetricsBLEEMEO,
} from "../Metric/DefaultDashboardMetrics";
import { chartTypes, useIntersection } from "../utils";
import { Fact } from "../Data/data.interface";
import { Box, Container, Flex, Grid, SimpleGrid } from "@chakra-ui/react";
import { ServicesList } from "../UI/ServicesList";
import { LastLogsList } from "../UI/LastLogsList";

type AgentSystemDashboardProps = {
  facts: Fact[];
};

const AgentSystemDashboard: FC<AgentSystemDashboardProps> = ({ facts }) => {
  let gaugesBar: GaugeBar[] = [];
  let numberMetrics: NumberMetric[] = [];

  if (facts.find((f) => f.name === "metrics_format")?.value === "Bleemeo") {
    gaugesBar = gaugesBarBLEEMEO;
    numberMetrics = numberMetricsBLEEMEO;
  } else if (
    facts.find((f) => f.name === "metrics_format")?.value == "Prometheus"
  ) {
    if (facts.find((f) => f.name === "kernel")?.value == "Linux") {
      gaugesBar = gaugesBarPrometheusLinux;
    } else {
      gaugesBar = gaugesBarPrometheusWindows;
    }
  }

  const triggerRef = useRef<HTMLDivElement>(null);
  const isVisible = useIntersection(triggerRef, "0px");
  const metricsWidgetMaxHeight = "14rem";
  const otherMetricsWidgetMaxHeight = "16rem";

  useEffect(() => {
    document.title = "Dashboard | Glouton";
  }, []);

  return (
    <>
      <Container h="100%">
        <Flex direction="column" h="100%">
          <SimpleGrid columns={gaugesBar.length} spacing={5}>
            {gaugesBar.map((gaugeItem) => (
              <Box ref={triggerRef} key={gaugeItem.title}>
                {isVisible ? (
                  <WidgetDashboardItem
                    type={chartTypes[0]}
                    title={gaugeItem.title}
                    metrics={gaugeItem.metrics}
                    unit={gaugeItem.unit}
                    period={{ minutes: 60 }}
                  />
                ) : (
                  <MetricGaugeItem name={gaugeItem.title} loading />
                )}
              </Box>
            ))}
          </SimpleGrid>

          <SimpleGrid columns={numberMetrics.length} spacing={5} mt={5}>
            {numberMetrics.map((numberMetric) => (
              <Box
                h={metricsWidgetMaxHeight}
                ref={triggerRef}
                key={numberMetric.title}
              >
                {isVisible ? (
                  numberMetric.metrics?.length > 1 ? (
                    <WidgetDashboardItem
                      type={chartTypes[2]}
                      title={numberMetric.title}
                      metrics={numberMetric.metrics}
                      unit={numberMetric.unit}
                      period={{ minutes: 60 }}
                    />
                  ) : (
                    <WidgetDashboardItem
                      type={chartTypes[1]}
                      title={numberMetric.title}
                      metrics={numberMetric.metrics}
                      unit={numberMetric.unit}
                      period={{ minutes: 60 }}
                    />
                  )
                ) : (
                  <MetricGaugeItem name={numberMetric.title} loading />
                )}
              </Box>
            ))}
          </SimpleGrid>

          <Grid templateColumns="7fr 9fr" gap={5} mt={5}>
            <Box h="fit-content">
              <ServicesList />
            </Box>
            <Box h={otherMetricsWidgetMaxHeight}>
              {/* TODO : Let the limit be editable */}
              <LastLogsList />
            </Box>
          </Grid>
        </Flex>
      </Container>
    </>
  );
};

export default AgentSystemDashboard;
