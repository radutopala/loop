import { getApiUrl } from "./api";

/** Call the browser action API for control operations (navigate, tabs, etc). */
export async function browserAction(
  channelId: string,
  action: string,
  params?: Record<string, unknown>,
): Promise<{
  result?: string;
  error?: string;
  tabs?: { target_id: string; url: string; title: string; active?: boolean }[];
  page_info?: { url: string; title: string };
}> {
  const res = await fetch(`${getApiUrl()}/api/browser/action`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, action, params }),
  });
  return res.json();
}

/**
 * Wipe the channel's persistent browser profile, destroying every login the
 * agent has. Tears the sidecar down first; the next use starts clean.
 */
export async function resetBrowserProfile(channelId: string): Promise<void> {
  await fetch(`${getApiUrl()}/api/browser/profile/reset`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId }),
  });
}

/** Switch browser mode between docker and host Chrome. */
export async function switchBrowserMode(channelId: string, mode: "docker" | "host"): Promise<{ mode: string }> {
  const res = await fetch(`${getApiUrl()}/api/browser/mode`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, mode }),
  });
  return res.json();
}

export interface CookieDomain {
  domain: string;
  count: number;
}

export interface CookieSource {
  id: string;
  browser: string;
  name: string;
  domains?: CookieDomain[];
  /** Set when this one profile could not be read; the others still are. */
  error?: string;
}

/**
 * List the browser profiles on this machine and the cookie scopes each holds.
 * On macOS the first call raises the Keychain prompt. Counts and categories
 * only — cookie values never cross this boundary.
 */
export async function listCookieSources(channelId: string): Promise<CookieSource[]> {
  const res = await fetch(`${getApiUrl()}/api/browser/cookies/sources?channel_id=${encodeURIComponent(channelId)}`);
  if (!res.ok) throw new Error((await res.text()) || "failed to list cookie sources");
  return res.json();
}

/** Import the chosen cookie scopes into the channel's browser profile. */
export async function importCookies(channelId: string, source: string, domains: string[]): Promise<{ imported: number; domains: number }> {
  const res = await fetch(`${getApiUrl()}/api/browser/cookies/import`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, source, domains }),
  });
  if (!res.ok) throw new Error((await res.text()) || "cookie import failed");
  return res.json();
}
