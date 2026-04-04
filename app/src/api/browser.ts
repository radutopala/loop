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

/** Switch browser mode between docker and host Chrome. */
export async function switchBrowserMode(
  channelId: string,
  mode: "docker" | "host",
): Promise<{ mode: string }> {
  const res = await fetch(`${getApiUrl()}/api/browser/mode`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ channel_id: channelId, mode }),
  });
  return res.json();
}
