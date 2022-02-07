import {
  ApolloClient,
  ApolloLink,
  HttpLink,
  InMemoryCache,
} from "@apollo/client";
import apolloLogger from "apollo-link-logger";

const GRAPHQLURL = "/graphql";

const links = [new HttpLink({ uri: GRAPHQLURL })];
if (process.env.NODE_ENV === "development") {
  links.unshift(apolloLogger);
}

const client = new ApolloClient({
  link: ApolloLink.from(links),
  cache: new InMemoryCache(),
});

export default client;
