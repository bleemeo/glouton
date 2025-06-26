import React, { FC, useEffect, useRef } from "react";

import WidgetDashboardItem from "../UI/WidgetDashboardItem";
import MetricGaugeItem from "../Metric/MetricGaugeItem";

import {
  gaugesBarBLEEMEO,
  numberMetricsBLEEMEO,
} from "../Metric/DefaultDashboardMetrics";
import { chartTypes, useIntersection } from "../utils";
import { Box, Flex, Grid, SimpleGrid } from "@chakra-ui/react";
import { ServicesList } from "../UI/ServicesList";
import { LastLogsList } from "../UI/LastLogsList";

const AgentSystemDashboard: FC = () => {
  const triggerRef = useRef<HTMLDivElement>(null);
  const isVisible = useIntersection(triggerRef, "0px");
  const otherMetricsWidgetMaxHeight = "40vh";

  useEffect(() => {
    document.title = "Dashboard | Glouton";
  }, []);

  return (
    <Flex direction="column" h="100%">
      <SimpleGrid columns={gaugesBarBLEEMEO.length} gap={5}>
        {gaugesBarBLEEMEO.map((gaugeItem) => (
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

      <SimpleGrid columns={numberMetricsBLEEMEO.length} gap={5} mt={5}>
        {numberMetricsBLEEMEO.map((numberMetric) => (
          <Box ref={triggerRef} key={numberMetric.title}>
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
  );
};

export default AgentSystemDashboard;
