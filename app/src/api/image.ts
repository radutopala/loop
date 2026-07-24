import type { DockerReclaimResult, ImageStatusResponse } from "../types";
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

// reclaimDockerSpace prunes unused BuildKit cache and dangling images, returning
// the bytes freed. Build-cache pruning is daemon-wide, not scoped to Loop.
export async function reclaimDockerSpace(): Promise<DockerReclaimResult> {
  const resp = await fetch(`${getApiUrl()}/api/image/reclaim`, { method: "POST" });
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}
