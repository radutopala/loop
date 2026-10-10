import type { DockerReclaimable, DockerReclaimResult, ImageStatusResponse } from "../types";
import { apiFetch, getApiUrl } from "./api";

export async function getImageStatus(): Promise<ImageStatusResponse> {
  const resp = await apiFetch(`${getApiUrl()}/api/image/status`);
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}

export async function rebuildImage(): Promise<void> {
  const resp = await apiFetch(`${getApiUrl()}/api/image/rebuild`, { method: "POST" });
  if (!resp.ok) throw new Error(await resp.text());
}

export async function removeImage(): Promise<void> {
  const resp = await apiFetch(`${getApiUrl()}/api/image`, { method: "DELETE" });
  if (!resp.ok) throw new Error(await resp.text());
}

// getReclaimable estimates, by source, what reclaimDockerSpace would free.
// Volumes are sized only with volumeSizes, which takes minutes on a large
// daemon.
export async function getReclaimable(volumeSizes = false): Promise<DockerReclaimable> {
  const resp = await apiFetch(`${getApiUrl()}/api/image/reclaimable${volumeSizes ? "?volume_sizes=true" : ""}`);
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}

// reclaimDockerSpace prunes unused BuildKit cache, dangling images, unused
// anonymous volumes and deleted channels' Chrome profiles, plus the tagged
// images no container uses when unusedImages is set, returning the bytes
// freed. Build-cache pruning is daemon-wide, not scoped to Loop.
export async function reclaimDockerSpace(unusedImages = false): Promise<DockerReclaimResult> {
  const resp = await apiFetch(`${getApiUrl()}/api/image/reclaim`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ unused_images: unusedImages }),
  });
  if (!resp.ok) throw new Error(await resp.text());
  return resp.json();
}
