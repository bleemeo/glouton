import React, { FC } from "react";
import { Card, CardBody, CardFooter, Flex, Text } from "@chakra-ui/react";

import { unitFormatCallback } from "../utils/formater";

type MetricNumberItemProps = {
  value: number;
  title: string;
  unit?: number;
};

const MetricNumberItem: FC<MetricNumberItemProps> = ({
  value,
  title,
  unit,
}) => {
  let formattedValue = unitFormatCallback(unit)(value);
  if (formattedValue === undefined) {
    formattedValue = "\u00A0\u00A0\u00A0";
  }

  return (
    <Card h="100%">
      <CardBody h="80%">
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="center"
          justify="center"
          wrap="nowrap"
        >
          <Flex w="100%" h="100%" align="center" justify="center">
            <Text mt={-3} fontSize="7vw" as="b" mb={0}>
              {formattedValue}
            </Text>
          </Flex>
        </Flex>
      </CardBody>
      <CardFooter h="20%" justify="center" p={0}>
        <Text fontSize="3xl" as="b">
          {title}
        </Text>
      </CardFooter>
    </Card>
  );
};

export default MetricNumberItem;
