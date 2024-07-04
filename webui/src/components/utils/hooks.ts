import { useEffect, useState } from "react";
import axios, { AxiosError } from "axios";

interface FetchParameters {
  [key: string]: any;
}

interface FetchResult<T> {
  data: T | null;
  isLoading: boolean;
  error: AxiosError | null;
  isFetching: boolean;
}

export const useHTTPDataFetch = <T,>(url: string, parameters: FetchParameters | null, pollInterval = 0): FetchResult<T> => {
  const [data, setData] = useState<T | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState<AxiosError | null>(null);
  const [isFetching, setIsFetching] = useState(false);

  useEffect(() => {
    const fetchData = async () => {
      setIsFetching(true);
      setIsError(null);

      try {
        const result = await axios.get<T>(url, { params: parameters });
        setData(result.data);
      } catch (error) {
        setIsError(error as AxiosError);
      } finally {
        setIsLoading(false);
        setIsFetching(false);
      }
    };

    fetchData();

    const intervalId = pollInterval > 0 ? setInterval(fetchData, pollInterval) : null;
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [url, JSON.stringify(parameters), pollInterval]);

  return { isLoading, error, data, isFetching };
};


export const useHTTPPromFetch = (urls, delay = 3000) => {
  const [data, setData] = useState<any[]>([]);
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
        let array_tmp: any[] = [];
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
