/** Whether url is the app's own page: the entry URL, whatever its hash (the app routes by hash). */
export function isAppEntryURL(url: string, entryURL: string): boolean {
  try {
    const target = new URL(url);
    const entry = new URL(entryURL);
    target.hash = "";
    entry.hash = "";
    return target.href === entry.href;
  } catch {
    return false;
  }
}

/** The fields of a permission request the decision reads. */
export interface PermissionRequestInfo {
  isMainFrame?: boolean;
  requestingUrl?: string;
  /** For a media request: the kinds asked for. */
  mediaTypes?: string[];
}

/** The fields of a permission check the decision reads. */
export interface PermissionCheckInfo {
  isMainFrame?: boolean;
  requestingUrl?: string;
  /** For a media check: "audio", "video" or "unknown". */
  mediaType?: string;
}

/**
 * Whether to grant a permission request. Media goes only to the app's own
 * page (its top frame, at the dev server or the packaged file URL) and only
 * for the microphone alone: no camera, no screen, nothing for a plugin's
 * frame or any other page. Every other permission is granted, as it was
 * before the app set a handler (Electron's default).
 */
export function allowPermissionRequest(permission: string, info: PermissionRequestInfo, entryURL: string): boolean {
  if (permission !== "media") return true;
  const types = info.mediaTypes ?? [];
  return info.isMainFrame === true && types.length > 0 && types.every((t) => t === "audio") && isAppEntryURL(info.requestingUrl ?? "", entryURL);
}

/** Whether a permission check passes, by the same rule as requests: media
 *  only for audio from the app's own top frame, anything else as before. */
export function allowPermissionCheck(permission: string, info: PermissionCheckInfo, entryURL: string): boolean {
  if (permission !== "media") return true;
  return info.isMainFrame === true && info.mediaType === "audio" && isAppEntryURL(info.requestingUrl ?? "", entryURL);
}
