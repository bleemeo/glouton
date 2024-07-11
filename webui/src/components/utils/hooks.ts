/* eslint-disable @typescript-eslint/no-explicit-any */
import { useEffect, useState } from "react";
import axios, { AxiosError } from "axios";
import { Metric, MetricFetchResult } from "../Metric/DefaultDashboardMetrics";
import { Log } from "../Data/data.interface";

interface FetchDataParameters {
  [key: string]: any;
}

interface FetchDataResult<T> {
  data: T | null;
  isLoading: boolean;
  error: AxiosError | null;
  isFetching: boolean;
}

export const useHTTPDataFetch = <T>(
  url: string,
  parameters: FetchDataParameters | null,
  pollInterval = 0,
): FetchDataResult<T> => {
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

    const intervalId =
      pollInterval > 0 ? setInterval(fetchData, pollInterval) : null;
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [url, JSON.stringify(parameters), pollInterval]);

  return { isLoading, error, data, isFetching };
};

interface FetchResult {
  data: MetricFetchResult[] | null;
  isLoading: boolean;
  error: AxiosError | null;
}

export const useHTTPPromFetch = (
  urls: string[],
  metrics: Metric[],
  delay = 3000,
): FetchResult => {
  const [data, setData] = useState<MetricFetchResult[] | null>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState(null);
  const [delayState, setIsDelayState] = useState(false);
  let time: any;

  useEffect(() => {
    async function fetchData() {
      time = setTimeout(() => setIsDelayState(true), delay);
      const res = await Promise.all(urls).then(async (values) => {
        const fetchData = async (url: string) => {
          setIsError(null);

          try {
            const result = await axios(url);

            setIsLoading(false);
            return result.data["data"]["result"];
          } catch (error) {
            setIsError(error);
          }
        };
        const array_tmp: any[] = [];
        for (const idx in values) {
          const arrayData = await fetchData(values[idx]);
          for (const value in arrayData) {
            arrayData[value].metric = metrics[idx];
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

export const useHTTPLogFetch = (limit: number, pollInterval: number) => {
  const [logs, setLogs] = useState<Log[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState<AxiosError | null>(null);
  const [isFetching, setIsFetching] = useState(false);

  useEffect(() => {
    const fetchData = async () => {
      setIsFetching(true);
      setIsError(null);

      try {
        const result = await axios.get(`/diagnostic.txt/log.txt`);
        const logPattern =
          /^(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}\.\d{1,12}) (.*)$/;
        const logsLines = result.data.split("\n").slice(0, -2); // Remove last 2 lines that are not logs
        const formattedLogs = logsLines
          .slice(logsLines.length - limit, logsLines.length)
          .map((line: string) => {
            const match = logPattern.exec(line);
            if (match) {
              return { timestamp: match[1], message: match[2] };
            }
            return { timestamp: "", message: line };
          });
        setLogs(formattedLogs);
      } catch (error) {
        setIsError(error as AxiosError);
      } finally {
        setIsLoading(false);
        setIsFetching(false);
      }
    };

    fetchData();

    const intervalId =
      pollInterval > 0 ? setInterval(fetchData, pollInterval) : null;
    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [pollInterval, limit]);

  return { isLoading, error, logs, isFetching };
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
