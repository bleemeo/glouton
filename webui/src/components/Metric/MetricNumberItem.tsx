import { FC } from "react";
import { Card, Flex, Text } from "@chakra-ui/react";

import { unitFormatCallback } from "../utils/formater";
import { Loading } from "../UI/Loading";
import QueryError from "../UI/QueryError";
import { isNil } from "lodash-es";

type MetricNumberItemProps = {
  value?: number;
  title: string;
  unit?: number;
  loading?: boolean;
  hasError?: object | null;
};

const MetricNumberItem: FC<MetricNumberItemProps> = ({
  value,
  title,
  unit,
  loading,
  hasError,
}) => {
  if (loading) {
    return (
      <Card.Root h="100%">
        <Card.Body>
          <Loading size="xl" />
        </Card.Body>
      </Card.Root>
    );
  } else if (hasError) {
    return (
      <Card.Root h="100%">
        <Card.Body>
          <QueryError noBorder style={{ textAlign: "center" }} />
        </Card.Body>
      </Card.Root>
    );
  }

  let formattedValue = unitFormatCallback(unit)(value!);
  if (isNil(formattedValue)) {
    formattedValue = "\u00A0\u00A0\u00A0";
  }

  return (
    <Card.Root h="100%">
      <Card.Body pb={0}>
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="center"
          justify="center"
          wrap="nowrap"
        >
          <Flex w="100%" h="100%" align="center" justify="center">
            <Text mt={-3} fontSize="min(10cqw, 20cqh)" as="b" mb={0}>
              {formattedValue}
            </Text>
          </Flex>
        </Flex>
      </Card.Body>
      <Card.Footer justifyContent="center" p={5}>
        <Text fontSize="3xl" as="b">
          {title}
        </Text>
      </Card.Footer>
    </Card.Root>
  );
};

export default MetricNumberItem;
