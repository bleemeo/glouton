import { useEffect, useRef, useState } from "react";

import { getJSON } from "./client";
import { notifyFetched } from "./freshness";
import type { PromQLResponse, StoreInfo } from "./types";

type FetchState<T> = {
  data: T | null;
  error: Error | null;
  loading: boolean;
};

/**
 * useFetch fetches a JSON URL, optionally re-fetching on a poll
 * interval. Loading flips to false on both success and error — the
 * earlier hook left it stuck on true after errors and the dashboard
 * spun forever.
 */
export function useFetch<T>(url: string | null, pollMs = 0): FetchState<T> {
  const [state, setState] = useState<FetchState<T>>({
    data: null,
    error: null,
    loading: url !== null,
  });

  useEffect(() => {
    if (url === null) {
      setState({ data: null, error: null, loading: false });

      return;
    }

    let cancelled = false;
    const ac = new AbortController();

    const run = async () => {
      try {
        const data = await getJSON<T>(url, ac.signal);

        if (!cancelled) {
          notifyFetched();
          setState({ data, error: null, loading: false });
        }
      } catch (err) {
        if (cancelled || (err as Error).name === "AbortError") {
          return;
        }

        setState((prev) => ({ data: prev.data, error: err as Error, loading: false }));
      }
    };

    void run();

    let timer: number | null = null;

    if (pollMs > 0) {
      timer = window.setInterval(run, pollMs);
    }

    return () => {
      cancelled = true;
      ac.abort();

      if (timer !== null) {
        window.clearInterval(timer);
      }
    };
  }, [url, pollMs]);

  return state;
}

/**
 * useStoreInfo polls /data/store-info every 30s. The UI uses it to
 * decide which time-range buttons are meaningful.
 */
export function useStoreInfo(): FetchState<StoreInfo> {
  return useFetch<StoreInfo>("/data/store-info", 30_000);
}

/**
 * usePromQLRange runs a query_range against the local API. `query` may
 * be null while the caller is still preparing inputs.
 */
export function usePromQLRange(
  query: string | null,
  rangeSeconds: number,
  stepSeconds: number,
  pollMs = 10_000,
): FetchState<PromQLResponse> {
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));

  // Pin "now" to a poll-aligned value so the URL is stable between
  // renders within the same poll interval — otherwise useEffect would
  // re-run on every render.
  const tickRef = useRef(now);

  useEffect(() => {
    if (query === null || pollMs <= 0) {
      return;
    }

    const t = window.setInterval(() => {
      const next = Math.floor(Date.now() / 1000);

      tickRef.current = next;
      setNow(next);
    }, pollMs);

    return () => window.clearInterval(t);
  }, [query, pollMs]);

  const start = now - rangeSeconds;
  const url =
    query === null
      ? null
      : `/api/v1/query_range?query=${encodeURIComponent(query)}&start=${start}&end=${now}&step=${stepSeconds}`;

  return useFetch<PromQLResponse>(url, 0);
}

/**
 * useTextFetch is the plain-text counterpart of useFetch. Used for
 * /data/logs which returns raw text rather than JSON.
 */
export function useTextFetch(url: string | null, pollMs = 0): FetchState<string> {
  const [state, setState] = useState<FetchState<string>>({
    data: null,
    error: null,
    loading: url !== null,
  });

  useEffect(() => {
    if (url === null) {
      setState({ data: null, error: null, loading: false });

      return;
    }

    let cancelled = false;
    const ac = new AbortController();

    const run = async () => {
      try {
        const res = await fetch(url, { signal: ac.signal });
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }

        const text = await res.text();

        if (!cancelled) {
          notifyFetched();
          setState({ data: text, error: null, loading: false });
        }
      } catch (err) {
        if (cancelled || (err as Error).name === "AbortError") {
          return;
        }

        setState((prev) => ({ data: prev.data, error: err as Error, loading: false }));
      }
    };

    void run();

    let timer: number | null = null;

    if (pollMs > 0) {
      timer = window.setInterval(run, pollMs);
    }

    return () => {
      cancelled = true;
      ac.abort();

      if (timer !== null) {
        window.clearInterval(timer);
      }
    };
  }, [url, pollMs]);

  return state;
}

export type { FetchState };
