// Tiny fetch helper. We deliberately avoid axios + tanstack-query; the
// surface we need (GET JSON with abort + small polling) is ~30 lines.

export class HttpError extends Error {
  status: number;
  body: string;

  constructor(status: number, body: string) {
    super(`HTTP ${status}: ${body.slice(0, 200)}`);
    this.status = status;
    this.body = body;
  }
}

export async function getJSON<T>(
  url: string,
  signal?: AbortSignal,
): Promise<T> {
  const res = await fetch(url, {
    signal,
    headers: { Accept: "application/json" },
  });

  if (!res.ok) {
    const text = await res.text().catch(() => "");

    throw new HttpError(res.status, text);
  }

  return (await res.json()) as T;
}
