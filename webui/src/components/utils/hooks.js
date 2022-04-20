import { useQuery } from "@apollo/client/react";
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

export const useHTTPFetch = (urls, delay = 3000) => {
  const [data, setData] = useState([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState(null);
  const [delayState, setIsDelayState] = useState(false);
  let time;

  useEffect(() => {
    async function fetchData() {
      time = setTimeout(() => setIsDelayState(true), delay);
      const res = await Promise.all(urls).then(async (values) => {
        const fetchData = async (url) => {
          setIsError(null);

          try {
            const result = await axios(url);

            setIsLoading(false);
            return result.data["data"]["result"];
          } catch (error) {
            setIsError(error);
          }
        };
        let array_tmp = [];
        for (let idx in values) {
          const arrayData = await fetchData(values[idx]);
          for (let value in arrayData) {
            arrayData[value].metric.legendId = idx;
            array_tmp.push(arrayData[value]);
          }
        }
        setIsDelayState(false);
        return array_tmp;
      });
      await setData(res);
    }
    fetchData();
  }, [delayState]);

  clearTimeout(time);
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
