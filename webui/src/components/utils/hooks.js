import { useQuery } from '@apollo/react-hooks'

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
    isLoading = loading && networkStatus !== 6
  }
  return { isLoading, error, ...data }
}
