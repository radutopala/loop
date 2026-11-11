import type { DictationStatus } from "../../api/dictation";
import { formatBytes } from "../../utils/formatBytes";

/** Where dictation stands for the composer's mic button. */
export type DictationAvailability = "unknown" | "disabled" | "unavailable" | "needs_model" | "downloading" | "ready";

export function dictationAvailability(s: DictationStatus | null): DictationAvailability {
  if (!s) return "unknown";
  if (!s.enabled) return "disabled";
  if (!s.available) return "unavailable";
  if (s.model_ready) return "ready";
  return s.download.active ? "downloading" : "needs_model";
}

/** The model's size for the download prompt, rounded to 50 MB ("~550 MB"). */
export function modelSizeLabel(s: DictationStatus | null): string {
  const bytes = s?.model_size_bytes || 0;
  if (bytes <= 0) return "large";
  const mb = Math.round(bytes / 1_000_000 / 50) * 50;
  return `~${Math.max(50, mb)} MB`;
}

/** The download's progress line, e.g. "Downloading… 42% (230 MB of 548 MB)". */
export function downloadProgressText(s: DictationStatus): string {
  const { received, total } = s.download;
  if (total <= 0) return received > 0 ? `Downloading… ${formatBytes(received)}` : "Downloading…";
  const pct = Math.min(100, Math.floor((received / total) * 100));
  return `Downloading… ${pct}% (${formatBytes(received)} of ${formatBytes(total)})`;
}

/** The hold key's name on this platform. */
export function dictationKeyName(isMac: boolean): string {
  return isMac ? "Right Option" : "Right Alt";
}

export function micButtonTip(a: DictationAvailability, s: DictationStatus | null, isMac: boolean): string {
  switch (a) {
    case "unavailable":
      return s?.reason === "binary_missing"
        ? "Dictation unavailable\nThe transcriber (whisper-server) isn't installed next to Loop or on PATH."
        : "Dictation unavailable\nThe daemon can't run the transcriber.";
    case "needs_model":
      return `Dictation\nNeeds a one-time ${modelSizeLabel(s)} model download; press to set it up.`;
    case "downloading":
      return `Dictation\n${s ? downloadProgressText(s) : "Downloading…"}`;
    default:
      return `Dictation\nHold ${dictationKeyName(isMac)} in the composer and speak; release to finish. Or click this button to start and again to stop. Esc cancels.`;
  }
}

export interface ModelDownloadDeps {
  start(): Promise<void>;
  status(): Promise<DictationStatus>;
  onProgress(s: DictationStatus): void;
  sleep(ms: number): Promise<void>;
  /** True once the caller no longer wants the result (unmounted). */
  cancelled?(): boolean;
}

export const MODEL_POLL_MS = 500;
/** Polls in a row that show no download, no error and no model before it's
 *  taken as stopped. */
const IDLE_POLLS = 10;

/**
 * Starts the model download and polls the status every 500 ms, passing each
 * one on, until the model is ready (resolves with that status) or the
 * download fails (rejects with its error). A cancelled caller resolves null.
 */
export async function downloadDictationModel(deps: ModelDownloadDeps): Promise<DictationStatus | null> {
  await deps.start();
  let idle = 0;
  for (;;) {
    if (deps.cancelled?.()) return null;
    const s = await deps.status();
    if (deps.cancelled?.()) return null;
    deps.onProgress(s);
    if (s.model_ready) return s;
    if (s.download.error) throw new Error(s.download.error);
    idle = s.download.active ? 0 : idle + 1;
    if (idle >= IDLE_POLLS) throw new Error("The model download stopped.");
    await deps.sleep(MODEL_POLL_MS);
  }
}
