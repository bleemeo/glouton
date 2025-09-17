/* eslint-disable @typescript-eslint/no-explicit-any */
import { useEffect, useMemo, useState } from "react";
import axios, { AxiosError } from "axios";
import { Metric, MetricFetchResult } from "../Metric/DefaultDashboardMetrics";
import { Log } from "../Data/data.interface";
import { debounce } from "lodash-es";

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

export const useHTTPLogFetch = (
  url: string,
  limit: number,
  pollInterval: number,
) => {
  const [logs, setLogs] = useState<Log[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setIsError] = useState<AxiosError | null>(null);
  const [isFetching, setIsFetching] = useState(false);

  useEffect(() => {
    const fetchData = async () => {
      setIsFetching(true);
      setIsError(null);

      try {
        const result = await axios.get(url);
        const logPattern =
          /^(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}\.\d{1,12}) (.*)$/;
        const logsLines = result.data.split("\n"); // Remove last 2 lines that are not logs
        const formattedLogs: Log[] = [];
        const logsLinesReversed: string[] = logsLines.reverse().slice(0, limit);
        let lastBrokenLine = "";
        logsLinesReversed.forEach((line: string) => {
          const match = logPattern.exec(line);
          if (match) {
            if (lastBrokenLine) {
              formattedLogs.push({
                timestamp: match[1],
                message: match[2] + lastBrokenLine,
              });
              lastBrokenLine = "";
            } else {
              formattedLogs.push({ timestamp: match[1], message: match[2] });
            }
          } else {
            lastBrokenLine = line;
          }
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

export const useDebounceValue = <T>(
  value: T,
  wait?: number,
  options?: object,
) => {
  const [internalValue, setInternalValue] = useState(value);

  const debouncedSetInternalValue = useMemo(() => {
    return debounce(setInternalValue, wait, options);
  }, [options, wait]);

  useEffect(() => {
    debouncedSetInternalValue(value);
  }, [debouncedSetInternalValue, value]);

  return internalValue;
};
