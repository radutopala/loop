import { pasteImage } from "../api/channels";

/**
 * The first image file on a clipboard payload, or null when there's none — so
 * callers can fall through to normal text paste. Synchronous so the caller can
 * decide to `preventDefault()` before kicking off the async upload.
 */
export function firstClipboardImage(data: DataTransfer | null | undefined): File | null {
  const items = data?.items;
  if (!items) return null;
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (it && it.kind === "file" && it.type.startsWith("image/")) {
      return it.getAsFile();
    }
  }
  return null;
}

/**
 * Upload a pasted image into the channel's workspace and return the saved file
 * path. Used by both the chat input and the agent terminal so a pasted image
 * lands as a `.loop/pastes/...` path the agent can read. The bytes are encoded
 * to base64 in chunks to avoid inflating a huge intermediate string.
 */
export async function uploadPastedImage(channelId: string, file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const bytes = new Uint8Array(buf);
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode.apply(null, Array.from(bytes.subarray(i, i + chunk)));
  }
  const base64 = btoa(binary);
  return pasteImage(channelId, base64, file.type);
}
