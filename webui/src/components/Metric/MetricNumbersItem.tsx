import React, { FC } from "react";

import {
  Flex,
  Card,
  Text,
  Spacer,
  SimpleGrid,
  Stat,
  HStack,
  FormatByte,
  Icon,
} from "@chakra-ui/react";
import { Loading } from "../UI/Loading";
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
  const formattedData = data!.map(
    (d: {
      value: number;
      legend: string;
      icon?: { name: string; color: string };
    }) => {
      return { value: d.value, legend: d.legend, icon: d.icon };
    },
  );

  return (
    <Card.Root h="100%">
      <Card.Body px={5} pt={1} pb={0} h="75%">
        <Flex
          w="100%"
          h="100%"
          align="right"
          direction="row"
          justify="center"
          justifyContent="space-between"
          flexWrap="wrap"
        >
          <SimpleGrid columns={Math.ceil(formattedData.length / 2)} gap={8}>
            {formattedData.map((d, idx) => (
              <Flex
                w="fit-content"
                h="fit-content"
                direction="column"
                key={idx}
                align="baseline"
              >
                <Stat.Root size={"lg"}>
                  <Stat.Label fontSize={"xl"}>{d.legend}</Stat.Label>
                  <HStack>
                    <Stat.ValueText fontSize={"6xl"} pt={5}>
                      {unit === 0 ? <FormatByte value={d.value} /> : d.value}
                    </Stat.ValueText>
                    <Icon size={"2xl"} mt={5} ml={3}>
                      {d.icon
                        ? iconFromName(d.icon.name, 8, 8, d.icon.color)
                        : ""}
                    </Icon>
                  </HStack>
                </Stat.Root>
                <Spacer />
              </Flex>
            ))}
          </SimpleGrid>
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

export default MetricNumbersItem;
