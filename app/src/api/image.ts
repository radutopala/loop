import type { ImageStatusResponse } from "../types";
import { getApiUrl } from "./api";

export async function getImageStatus(): Promise<ImageStatusResponse> {
  const resp = await fetch(`${getApiUrl()}/api/image/status`);
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}

export async function rebuildImage(): Promise<void> {
  const resp = await fetch(`${getApiUrl()}/api/image/rebuild`, { method: "POST" });
  if (!resp.ok) throw new Error(await resp.text());
}

export async function removeImage(): Promise<void> {
  const resp = await fetch(`${getApiUrl()}/api/image`, { method: "DELETE" });
  if (!resp.ok) throw new Error(await resp.text());
}
