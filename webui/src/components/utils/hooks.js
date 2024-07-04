import { useEffect, useState } from "react";
import axios from "axios";

export const useHTTPDataFetch = (url, parameters, pollInterval = 0) => {
  
  const [data, setData] = useState(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState(null);
  const [isFetching, setIsFetching] = useState(false); // New state to track ongoing fetch

  useEffect(() => {
    const fetchData = async () => {
      setIsFetching(true); // Set fetching to true
      setIsError(null);
      
      try {
        const result = await axios.get(url, { params: parameters });
        setData(result.data);
      } catch (error) {
        setIsError(error);
      } finally {
        setIsLoading(false); // Set loading to false only when the initial fetch completes
        setIsFetching(false); // Set fetching to false after fetch completes
      }
    };
    
    fetchData();

    const intervalId = pollInterval > 0 ? setInterval(fetchData, pollInterval) : null;
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [url, JSON.stringify(parameters), pollInterval]);

  return { isLoading, error, data, isFetching }; // Return isFetching as well
};


export const useHTTPPromFetch = (urls, delay = 3000) => {
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
