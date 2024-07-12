import React, { FC } from "react";

import { unitFormatCallback } from "../utils/formater";
import {
  Flex,
  CardFooter,
  CardBody,
  Card,
  Text,
  Spacer,
  SimpleGrid,
} from "@chakra-ui/react";
import Loading from "../UI/Loading";
import QueryError from "../UI/QueryError";
import { iconFromName } from "../utils";

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
  const formattedData = data!.map(
    (d: {
      value: number;
      legend: string;
      icon?: { name: string; color: string };
    }) => {
      console.log(d);
      const v = unitFormatCallback(unit)(d.value)
        ? unitFormatCallback(unit)(d.value)
        : "\u00A0\u00A0\u00A0";
      return { value: v, legend: d.legend, icon: d.icon };
    },
  );

  return (
    <Card h="100%">
      <CardBody px={5} pt={1} pb={0} h="75%">
        <Flex
          w="100%"
          h="100%"
          align="right"
          direction="row"
          justify="center"
          justifyContent="space-between"
          flexWrap="wrap"
        >
          <SimpleGrid columns={Math.ceil(formattedData.length / 2)} spacing={8}>
            {formattedData.map((d, idx) => (
              <Flex
                w="fit-content"
                h="fit-content"
                direction="column"
                key={idx}
                align="baseline"
              >
                <Text fontSize="md" mb={0}>
                  {d.legend}
                </Text>
                <Text
                  mt={-1}
                  lineHeight="80%"
                  fontSize="min(50cqw, 10cqh)"
                  as="b"
                  mb={0}
                  whiteSpace="nowrap"
                >
                  {d.value}{" "}
                  {d.icon ? iconFromName(d.icon.name, 8, 8, d.icon.color) : ""}
                </Text>
                <Spacer />
              </Flex>
            ))}
          </SimpleGrid>
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

export default MetricNumbersItem;
