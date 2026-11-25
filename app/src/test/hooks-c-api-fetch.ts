import { type Mock, vi } from "vitest";

/** The signature of fetch, which the api modules call through apiFetch. */
export type FetchMock = Mock<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>;

/** Base URL apiFetch targets when no desktop bridge or probe changed it. */
export const API = "http://localhost:8222";

/** Stubs the globals the api modules read (window, sessionStorage, fetch) and
 *  returns the fetch mock. Pair with vi.unstubAllGlobals() in afterEach. */
export function stubFetch(): FetchMock {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  const fetchMock: FetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

/** A JSON response. */
export function json(body: unknown, init?: ResponseInit): Response {
  return new Response(JSON.stringify(body), init);
}

/** A failed response with a body and status text. */
export function fail(status: number, statusText: string, body = ""): Response {
  return new Response(body, { status, statusText });
}

/** The URL, method, headers and parsed body of the nth fetch call. */
export function sent(fetchMock: FetchMock, n = 0): { url: string; method: string; headers: Headers; body: unknown } {
  const call = fetchMock.mock.calls[n];
  if (!call) throw new Error(`no fetch call #${n}`);
  const [input, init] = call;
  const raw = init?.body;
  let body: unknown = raw;
  if (typeof raw === "string") {
    try {
      body = JSON.parse(raw);
    } catch {
      body = raw;
    }
  }
  return { url: String(input), method: init?.method ?? "GET", headers: new Headers(init?.headers), body };
}

/** A failed response whose body can't be read, for the text().catch paths.
 *  clone returns itself, as apiFetch hands a GET back as a clone. */
export function unreadable(status: number, statusText: string): Response {
  const res = { ok: false, status, statusText, text: () => Promise.reject(new Error("body lost")), clone: () => res };
  return res as unknown as Response;
}
