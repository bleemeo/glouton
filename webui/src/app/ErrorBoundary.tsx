import { Box, Code, Heading, Text, VStack } from "@chakra-ui/react";
import { Component, type ReactNode } from "react";

type Props = { children: ReactNode };
type State = { error: Error | null };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error("UI crashed:", error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <Box p="8" maxW="800px" mx="auto">
          <VStack align="start" gap="3">
            <Heading size="md" color="status.crit">
              Something went wrong rendering the panel.
            </Heading>
            <Text color="fg.muted">
              The error is logged to the browser console. Reloading usually
              helps.
            </Text>
            <Code variant="surface" p="3" w="full" whiteSpace="pre-wrap">
              {this.state.error.message}
            </Code>
          </VStack>
        </Box>
      );
    }

    return this.props.children;
  }
}
