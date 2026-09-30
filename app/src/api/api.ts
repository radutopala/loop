let apiUrl = "http://localhost:8222";

// When running in a browser (not Electron), probe for the API server.
// Try same-origin first (works when Vite proxies /api), then external URLs.
async function probeApiUrl(): Promise<void> {
  if (typeof window === "undefined") return;
  const candidates = [
    window.location.origin, // same-origin (Vite proxy or co-located server)
    "http://host.docker.internal:8222",
    "http://localhost:8222",
  ];
  for (const url of candidates) {
    try {
      const res = await fetch(`${url}/api/health`, { signal: AbortSignal.timeout(1000) });
      if (res.ok) {
        apiUrl = url;
        return;
      }
    } catch {
      /* try next */
    }
  }
}

export async function initApiUrl(): Promise<void> {
  if (window.loopAPI) {
    apiUrl = await window.loopAPI.getApiUrl();
  } else {
    await probeApiUrl();
  }
  await refreshApiToken();
}

export function getApiUrl(): string {
  return apiUrl;
}

export function getWsUrl(): string {
  return apiUrl.replace(/^http/, "ws");
}

// ── Authentication ──
//
// Every API call carries the owner token. The desktop app reads it from the
// file the daemon writes (through the preload, fresh on each ask so a
// rotation is picked up); a plain browser tab gets it once from the URL
// fragment `#loop_token=…` (printed by `loop app:url`) and keeps it for the
// tab in sessionStorage.

const TOKEN_KEY = "loop-api-token";
const TOKEN_PARAM = "loop_token=";

let apiToken = "";

/** Move a `#loop_token=` fragment into sessionStorage and drop it from the
 *  URL, so it doesn't linger in history or get read as a channel link. Run
 *  before anything reads location.hash. */
export function bootstrapTokenFromHash(): boolean {
  const hash = window.location.hash;
  if (!hash.startsWith(`#${TOKEN_PARAM}`)) return false;
  const tok = decodeURIComponent(hash.slice(TOKEN_PARAM.length + 1));
  history.replaceState(null, "", window.location.pathname + window.location.search);
  if (!tok || tok === sessionStorage.getItem(TOKEN_KEY)) return false;
  sessionStorage.setItem(TOKEN_KEY, tok);
  return true;
}

/** Take a token pasted into an already-open tab: a URL that differs only by
 *  its fragment doesn't reload the page, so bootstrapTokenFromHash never
 *  sees it. Reload once a new one is stored so everything loads again with
 *  it; the same token again only leaves the URL. */
export function watchTokenHash(): void {
  window.addEventListener("hashchange", () => {
    if (bootstrapTokenFromHash()) window.location.reload();
  });
}

/** Read the token again from where it lives. */
export async function refreshApiToken(): Promise<string> {
  if (window.loopAPI?.getApiToken) {
    apiToken = (await window.loopAPI.getApiToken()) || "";
  } else {
    apiToken = sessionStorage.getItem(TOKEN_KEY) || "";
  }
  return apiToken;
}

export function getApiToken(): string {
  return apiToken;
}

function withAuth(init: RequestInit | undefined, tok: string): RequestInit {
  if (!tok) return init ?? {};
  const headers = new Headers(init?.headers);
  headers.set("Authorization", `Bearer ${tok}`);
  return { ...init, headers };
}

/** fetch for the Loop API: adds the token, and on a 401 reads the token
 *  again (it may have been rotated, or the daemon just wrote it) and retries
 *  once. Bodies must be replayable (strings, not streams). */
export async function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  const tok = apiToken || (await refreshApiToken());
  const res = await fetch(input, withAuth(init, tok));
  if (res.status !== 401) return res;
  const fresh = await refreshApiToken();
  if (!fresh || fresh === tok) return res;
  return fetch(input, withAuth(init, fresh));
}

/** WebSocket subprotocols that carry the token: the server answers with
 *  "loop" and reads the token from the second. */
export function wsProtocols(): string[] {
  return apiToken ? ["loop", `loop.token.${apiToken}`] : ["loop"];
}

// ── Content links ──
//
// Things the browser loads by URL (iframes, <img>, <video>, <base href>)
// can't send a header, so they use short-lived signed links from
// POST /api/content-caps, cached per scope and minted again before they
// expire or once the daemon forgets them (a restart).

export type ContentCapScope = { kind: "raw"; channelId: string; root: number } | { kind: "playground"; channelId?: string; name: string };

interface CachedCap {
  base: string;
  expiresAt: number;
}

const caps = new Map<string, Promise<CachedCap>>();

function capKey(scope: ContentCapScope): string {
  return scope.kind === "raw" ? `raw\0${scope.channelId}\0${scope.root}` : `playground\0${scope.channelId ?? ""}\0${scope.name}`;
}

async function mintCap(scope: ContentCapScope): Promise<CachedCap> {
  const body = scope.kind === "raw" ? { kind: "raw", channel_id: scope.channelId, root: scope.root } : { kind: "playground", channel_id: scope.channelId ?? "", name: scope.name };
  const res = await apiFetch(`${apiUrl}/api/content-caps`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`Failed to get a content link: ${res.statusText}`);
  const data: { base_url: string; expires_in_sec: number } = await res.json();
  // Renew at 80% of the lifetime so a link handed out is never about to expire.
  return { base: `${apiUrl}${data.base_url}`, expiresAt: Date.now() + data.expires_in_sec * 800 };
}

/** The base URL (ending in "/") of a content link for scope. */
export async function contentCapBase(scope: ContentCapScope): Promise<string> {
  const key = capKey(scope);
  const cached = caps.get(key);
  if (cached) {
    try {
      const c = await cached;
      if (c.expiresAt > Date.now()) return c.base;
    } catch {
      /* mint again */
    }
  }
  const p = mintCap(scope);
  caps.set(key, p);
  return (await p).base;
}

/** Forget the content link of one scope, e.g. after a load through it failed
 *  because it expired. The next contentCapBase call mints a fresh one. */
export function forgetContentCap(scope: ContentCapScope): void {
  caps.delete(capKey(scope));
}

// Bumped by clearContentCaps so views holding a link (an iframe src, a
// <base href>, an <img> src) can resolve it again.
let capsEpoch = 0;
const capsListeners = new Set<() => void>();

/** Counter bumped each time every content link is forgotten. */
export function contentCapsEpoch(): number {
  return capsEpoch;
}

/** Calls listener whenever every content link is forgotten; returns the
 *  unsubscribe function. */
export function subscribeContentCaps(listener: () => void): () => void {
  capsListeners.add(listener);
  return () => {
    capsListeners.delete(listener);
  };
}

/** Forget every content link, e.g. after the daemon restarted with a new key,
 *  and tell the views holding one to resolve it again. */
export function clearContentCaps(): void {
  caps.clear();
  capsEpoch++;
  for (const l of capsListeners) l();
}

/** A URL path under a content link: each segment encoded, slashes kept. */
export function encodePathSegments(path: string): string {
  return path.split("/").map(encodeURIComponent).join("/");
}
