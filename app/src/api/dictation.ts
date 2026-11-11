import { apiFetch, getApiUrl, getWsUrl, wsProtocols } from "./api";

/** The daemon's dictation state: whether the transcriber is installed, and
 *  whether its model is on disk or being downloaded. */
export interface DictationStatus {
  /** dictation.enabled: off, the mic button hides and the hold key types. */
  enabled: boolean;
  available: boolean;
  /** Why it isn't available: "" or "binary_missing". */
  reason: string;
  model: string;
  model_ready: boolean;
  model_size_bytes: number;
  download: { active: boolean; received: number; total: number; error: string };
  running: boolean;
  /** dictation.send_phrases: said last, they send the message. */
  send_phrases: string[] | null;
}

export async function fetchDictationStatus(): Promise<DictationStatus> {
  const res = await apiFetch(`${getApiUrl()}/api/dictation/status`);
  if (!res.ok) throw new Error(`Failed to fetch dictation status: ${res.statusText}`);
  return res.json();
}

/** Starts the one-time model download; progress shows in the status. */
export async function startDictationModelDownload(): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/dictation/model`, { method: "POST" });
  if (!res.ok) {
    const body = (await res.text().catch(() => "")).trim();
    throw new Error(body || `Failed to start the model download: ${res.statusText}`);
  }
}

/** Opens the dictation stream, authenticated like the app's other daemon
 *  sockets (the "loop" subprotocol plus the token one). */
export function openDictationSocket(language?: string): WebSocket {
  const q = language ? `?language=${encodeURIComponent(language)}` : "";
  const ws = new WebSocket(`${getWsUrl()}/api/dictation/stream${q}`, wsProtocols());
  ws.binaryType = "arraybuffer";
  return ws;
}
