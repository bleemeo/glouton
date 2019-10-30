import { useQuery } from '@apollo/react-hooks'

export const POLL = 6

export const useFetch = (query, variables = null, pollInterval = 0) => {
  const fetchConfig = {
    fetchPolicy: 'network-only'
  }
  if (variables) fetchConfig.variables = variables
  if (pollInterval) {
    fetchConfig.pollInterval = pollInterval
    fetchConfig.notifyOnNetworkStatusChange = true
  }
  const { loading, error, data, networkStatus } = useQuery(query, fetchConfig)
  let isLoading = loading
  if (pollInterval && networkStatus) {
    isLoading = loading && networkStatus !== POLL
  }
  return { isLoading, error, ...data, networkStatus }
}
