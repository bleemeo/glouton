// Tracks the timestamp of the most recent successful fetch made by
// useFetch. The header consumes this to show a "Updated Xs ago"
// indicator, so it has to be a global signal rather than per-component
// state — one signal across all polling hooks on the active page.

type Listener = () => void;

let lastFetchAt = 0;
const listeners = new Set<Listener>();

export function notifyFetched() {
  lastFetchAt = Date.now();
  for (const l of listeners) l();
}

export function subscribeFreshness(cb: Listener): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function getLastFetchAt(): number {
  return lastFetchAt;
}
