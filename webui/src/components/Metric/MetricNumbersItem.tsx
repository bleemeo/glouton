import React, { FC, useEffect, useRef } from "react";

import { unitFormatCallback } from "../utils/formater";
import {
  Flex,
  Box,
  CardFooter,
  CardBody,
  Card,
  Text,
  Spacer,
} from "@chakra-ui/react";

const SIZE = 100;

type MetricNumberItemProps = {
  data: { value: number; legend: string }[];
  title: string;
  unit?: number;
};

const MetricNumbersItem: FC<MetricNumberItemProps> = ({
  data,
  title,
  unit,
}) => {
  const svgElem = useRef<SVGSVGElement | null>(null);
  const textElem = useRef<SVGTextElement | null>(null);

  const resize = () => {
    if (svgElem.current && textElem.current) {
      const svg = svgElem.current;
      const svgCTM = svg.getScreenCTM();
      const textBBox = textElem.current.getBBox();
      if (!svgCTM) {
        return;
      }
      const svgHeight =
        (svg.parentNode as HTMLElement)?.clientHeight / svgCTM.a;
      const svgWidth = (svg.parentNode as HTMLElement)?.clientWidth / svgCTM.a;

      let textHeight = textBBox.height;
      if (textHeight === 0) {
        textHeight = 1;
      }

      let textWidth = textBBox.width;
      if (textWidth === 0) {
        textWidth = 1;
      }

      const ratio = Math.min(svgHeight / textHeight, svgWidth / textWidth);
      const halfSize = SIZE / 2;
      const translateRatio = -halfSize * (ratio - 1);
      textElem.current.setAttribute(
        "transform",
        `matrix(${ratio}, 0, 0, ${ratio}, ${translateRatio}, ${translateRatio})`,
      );
    }
  };

  useEffect(() => {
    resize();
  });

  const formattedData = data.map((d: { value: number; legend: string }) => {
    const v = unitFormatCallback(unit)(d.value)
      ? unitFormatCallback(unit)(d.value)
      : "\u00A0\u00A0\u00A0";
    return { value: v, legend: d.legend };
  });

  return (
    <Card h="100%">
      <CardBody pb={0}>
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="center"
          justify="center"
          wrap="nowrap"
        >
          <Box w="100%" h="100%">
            {formattedData.map((d, idx) => (
              <Flex direction="column" key={idx} align="baseline">
                <Text fontSize="md" mb={0}>
                  {d.legend}
                </Text>
                <Text mt={-3} fontSize="5xl" as="b" mb={0}>
                  {d.value}
                </Text>
                <Spacer />
              </Flex>
            ))}
          </Box>
        </Flex>
      </CardBody>
      <CardFooter justify="center" p={0}>
        <Text fontSize="3xl" as="b">
          {title}
        </Text>
      </CardFooter>
    </Card>
  );
};

export default MetricNumbersItem;
