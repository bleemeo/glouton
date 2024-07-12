import React, { FC } from "react";
import { Card, CardBody, CardFooter, Flex, Text } from "@chakra-ui/react";

import { unitFormatCallback } from "../utils/formater";
import Loading from "../UI/Loading";
import QueryError from "../UI/QueryError";

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
      <Card h="100%">
        <CardBody>
          <Loading size="xl" />
        </CardBody>
      </Card>
    );
  } else if (hasError) {
    return (
      <Card h="100%">
        <CardBody>
          <QueryError noBorder style={{ textAlign: "center" }} />
        </CardBody>
      </Card>
    );
  }

  let formattedValue = unitFormatCallback(unit)(value!);
  if (formattedValue === undefined) {
    formattedValue = "\u00A0\u00A0\u00A0";
  }

  return (
    <Card h="100%">
      <CardBody h="75%" pb={0}>
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="center"
          justify="center"
          wrap="nowrap"
        >
          <Flex w="100%" h="100%" align="center" justify="center">
            <Text mt={-3} fontSize="min(60cqw, 20cqh)" as="b" mb={0}>
              {formattedValue}
            </Text>
          </Flex>
        </Flex>
      </CardBody>
      <CardFooter justify="center" p={0}>
        <Text fontSize="5vh" as="b">
          {title}
        </Text>
      </CardFooter>
    </Card>
  );
};

export default MetricNumberItem;
