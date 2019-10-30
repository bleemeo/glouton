import ApolloClient from 'apollo-client'
import { HttpLink } from 'apollo-link-http'
import { ApolloLink } from 'apollo-link'
import apolloLogger from 'apollo-link-logger'
import { InMemoryCache } from 'apollo-cache-inmemory'

const GRAPHQLURL = '/graphql'

const links = [new HttpLink({ uri: GRAPHQLURL })]
if (process.env.NODE_ENV === 'development') {
  links.unshift(apolloLogger)
}

const client = new ApolloClient({
  link: ApolloLink.from(links),
  cache: new InMemoryCache()
})

export default client
