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
}

export function getApiUrl(): string {
  return apiUrl;
}

export function getWsUrl(): string {
  return apiUrl.replace(/^http/, "ws");
}
