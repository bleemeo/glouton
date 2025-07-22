import DonutPieChart from "../UI/DonutPieChart";
import { Loading } from "../UI/Loading";
import QueryError from "../UI/QueryError";

import { colorForStatus } from "../utils/converter";
import { unitFormatCallback } from "../utils/formater";
import { Card, Flex, Text } from "@chakra-ui/react";

type MetricGaugeItemProps = {
  unit?: number;
  value?: number;
  name: string;
  fontSize?: number;
  loading?: boolean;
  hasError?: object | null;
  thresholds?: {
    highWarning?: number;
    highCritical?: number;
  };
};

const MetricGaugeItem = ({
  unit,
  value,
  name,
  fontSize,
  loading,
  hasError,
  thresholds,
}: MetricGaugeItemProps) => {
  if (loading) {
    return (
      <Card.Root>
        <Card.Body>
          <Loading size="xl" />
        </Card.Body>
      </Card.Root>
    );
  } else if (hasError) {
    return (
      <Card.Root>
        <Card.Body>
          <QueryError noBorder style={{ textAlign: "center" }} />
        </Card.Body>
      </Card.Root>
    );
  }
  const segmentsStep: number[] = [0];
  const segmentsColor = ["#" + colorForStatus(0)];

  if (thresholds) {
    if (thresholds.highWarning) {
      segmentsStep.push(thresholds.highWarning);
      segmentsColor.push("#" + colorForStatus(1));
    }
    if (thresholds.highCritical) {
      segmentsStep.push(thresholds.highCritical);
      segmentsColor.push("#" + colorForStatus(2));
    }
  }

  segmentsStep.push(100);
  segmentsColor.push("#" + colorForStatus(3));

  return (
    <Card.Root>
      <Card.Body pb={0}>
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="center"
          justify="center"
          wrap="nowrap"
          p={1}
        >
          <DonutPieChart
            value={value ? value : 0}
            fontSize={fontSize ? fontSize : 12}
            segmentsStep={segmentsStep}
            segmentsColor={segmentsColor}
            formattedValue={
              unitFormatCallback(unit)(value)
                ? unitFormatCallback(unit)(value)!
                : "N/A"
            }
          />
        </Flex>
      </Card.Body>
      <Card.Footer justifyContent="center" p={5}>
        <Text fontSize="3xl" as="b">
          {name}
        </Text>
      </Card.Footer>
    </Card.Root>
  );
};

export default MetricGaugeItem;
