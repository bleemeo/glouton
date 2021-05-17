import { useQuery } from "@apollo/react-hooks";
import { useEffect, useState } from "react";
import axios from "axios";

export const POLL = 6;

export const useFetch = (query, variables, pollInterval = 0) => {
  const fetchConfig = {
    fetchPolicy: "network-only",
  };
  if (variables) fetchConfig.variables = variables;
  if (pollInterval) {
    fetchConfig.pollInterval = pollInterval;
    fetchConfig.notifyOnNetworkStatusChange = true;
  }
  const { loading, error, data, networkStatus } = useQuery(query, fetchConfig);
  let isLoading = loading;
  if (pollInterval && networkStatus) {
    isLoading = loading && networkStatus !== POLL;
  }
  return { isLoading, error, ...data, networkStatus };
};

export const httpFetch = (variables) => {
  const [data, setData] = useState({});
  const url = `/api/v1/query_range?query=${encodeURIComponent(
    variables.query
  )}&start=${encodeURIComponent(variables.start)}&end=${encodeURIComponent(
    variables.end
  )}&step=${encodeURIComponent(variables.step)}`;
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState(null);

  useEffect(() => {
    const fetchData = async () => {
      setIsError(null);
      setIsLoading(true);

      try {
        const result = await axios(url);

        setData(result.data["data"]["result"]);
      } catch (error) {
        setIsError(error);
      }

      setIsLoading(false);
    };

    fetchData();
  }, [url]);

  return { data, isLoading, error };
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
