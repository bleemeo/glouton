import { useQuery } from "@apollo/react-hooks";
import { axios } from "axios";
import { useEffect, useState } from "react";

export const POLL = 6;

export const useFetch = (query, variables = null, pollInterval = 0) => {
  const fetchConfig = {
    fetchPolicy: "network-only",
  };
  if (query) {
    if (variables) fetchConfig.variables = variables;
    if (pollInterval) {
      fetchConfig.pollInterval = pollInterval;
      fetchConfig.notifyOnNetworkStatusChange = true;
    }
    const { loading, error, data, networkStatus } = useQuery(
      query,
      fetchConfig
    );
    let isLoading = loading;
    if (pollInterval && networkStatus) {
      isLoading = loading && networkStatus !== POLL;
    }
    return { isLoading, error, ...data, networkStatus };
  }
  const getMoviesFromApiAsync = async () => {
    try {
      let response = await fetch(
        `/api/v1/query_range?query=${encodeURIComponent(
          variables.query
        )}&start=${encodeURIComponent(
          variables.start
        )}&end=${encodeURIComponent(variables.end)}&step=${encodeURIComponent(
          variables.step
        )}`
      );
      let data = await response.json();
      let loading = false;
      let error = null;
      let networkStatus = 200;
      data = data.data["result"][0]["values"];
      return { loading, error, status, data, networkStatus };
    } catch (error) {
      console.error(error);
    }
  };
  const {
    loading,
    error,
    status,
    data,
    networkStatus,
  } = getMoviesFromApiAsync();
  let isLoading = loading;
  return { isLoading, error, status, data, networkStatus };
};

export const useWindowWidth = () => {
  const [windowWidth, setWindowWidth] = useState(window.innerWidth);

  const onWindowResize = () => {
    setWindowWidth(window.innerWidth);
  };
  useEffect(() => {
    window.addEventListener("resize", onWindowResize);
    return () => {
      window.removeEventListener("resize", onWindowResize);
    };
  }, []);

  return windowWidth;
};
