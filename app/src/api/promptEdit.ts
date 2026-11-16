import type { PromptEditRequest, PromptEditResult } from "../components/chat/dictationController";
import { apiFetch, getApiUrl } from "./api";

// Tells the Edits view an edit by voice ended, so it can find the channel's
// prompt-edit thread once the first edit has made it.
const settled = new EventTarget();
const SETTLED = "settled";

/** Rewrites a draft as a spoken instruction asks, on the model and effort
 *  the channel's composer has selected. The edit runs in the channel's
 *  hidden prompt-edit thread; onPromptEditSettled hears when it ends. */
export async function editPrompt(channelId: string, req: PromptEditRequest, signal?: AbortSignal): Promise<PromptEditResult> {
  try {
    const res = await apiFetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/prompt-edit`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
      signal,
    });
    if (!res.ok) {
      const body = (await res.text().catch(() => "")).trim();
      throw new Error(body || res.statusText || `HTTP ${res.status}`);
    }
    return await res.json();
  } finally {
    settled.dispatchEvent(new CustomEvent<string>(SETTLED, { detail: channelId }));
  }
}

/** Calls listener with the channel of each edit by voice that ends, done or
 *  not. Returns the unsubscribe. */
export function onPromptEditSettled(listener: (channelId: string) => void): () => void {
  const fn = (e: Event) => listener((e as CustomEvent<string>).detail);
  settled.addEventListener(SETTLED, fn);
  return () => settled.removeEventListener(SETTLED, fn);
}

/** The channel's hidden prompt-edit thread, whose chat holds its edits by voice. */
export interface PromptEditState {
  /** "" before the channel's first edit. */
  edit_channel_id: string;
}

export async function fetchPromptEditState(channelId: string): Promise<PromptEditState> {
  const res = await apiFetch(`${getApiUrl()}/api/channels/${encodeURIComponent(channelId)}/prompt-edit`);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}
