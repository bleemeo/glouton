import React, { FC } from "react";
import { Heading, Spinner, VStack } from "@chakra-ui/react";

export type LoadingProps = {
  size?: "sm" | "md" | "lg" | "xl";
  message?: string;
};

export const Loading: FC<LoadingProps> = ({
  size = "lg",
  message = "Loading...",
}) => {
  return (
    <VStack color={"bleemeo"}>
      <Spinner size={size} />
      <Heading size={size}>{message}</Heading>
    </VStack>
  );
};
