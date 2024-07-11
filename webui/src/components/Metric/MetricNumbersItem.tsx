import React, { FC } from "react";

import { unitFormatCallback } from "../utils/formater";
import {
  Flex,
  CardFooter,
  CardBody,
  Card,
  Text,
  Spacer,
} from "@chakra-ui/react";
import Loading from "../UI/Loading";
import QueryError from "../UI/QueryError";

type MetricNumberItemProps = {
  data?: { value: number; legend: string }[];
  title: string;
  unit?: number;
  loading?: boolean;
  hasError?: object | null;
};

const MetricNumbersItem: FC<MetricNumberItemProps> = ({
  data,
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
  const formattedData = data!.map((d: { value: number; legend: string }) => {
    const v = unitFormatCallback(unit)(d.value)
      ? unitFormatCallback(unit)(d.value)
      : "\u00A0\u00A0\u00A0";
    return { value: v, legend: d.legend };
  });

  return (
    <Card h="100%">
      <CardBody px={4} pt={2} pb={0} h="80%">
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="right"
          justify="center"
          flexWrap="wrap"
          justifyContent="flex-start"
        >
          {formattedData.map((d, idx) => (
            <Flex w="fit-content" direction="column" key={idx} align="baseline">
              <Text fontSize="md" mb={0}>
                {d.legend}
              </Text>
              <Text mt={-4} fontSize="5xl" as="b" mb={0}>
                {d.value}
              </Text>
              <Spacer />
            </Flex>
          ))}
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

export default MetricNumbersItem;
