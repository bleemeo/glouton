import React, { FC, useEffect, useRef } from "react";
import { Card, CardBody, CardFooter, Flex, Text } from "@chakra-ui/react";

import { unitFormatCallback } from "../utils/formater";

const SIZE = 100;

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

  let formattedValue = unitFormatCallback(unit)(value);
  if (formattedValue === undefined) {
    formattedValue = "\u00A0\u00A0\u00A0";
  }

  return (
    <Card h="100%">
      <CardBody>
        <Flex
          direction="column"
          w="100%"
          h="100%"
          align="center"
          justify="center"
          wrap="nowrap"
        >
          <Flex w="100%" h="100%" align="center" justify="center">
            <Text mt={-3} fontSize="7rem" as="b" mb={0}>
              {formattedValue}
            </Text>
          </Flex>
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

export default MetricNumberItem;
