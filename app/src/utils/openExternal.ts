/**
 * Open a URL in the user's external/system browser. In Electron this uses
 * shell.openExternal (avoiding an in-app about:blank popup); in a plain browser
 * (dev mode) it falls back to window.open with noopener.
 */
export function openExternalUrl(url: string): void {
  if (window.loopAPI?.openExternal) {
    void window.loopAPI.openExternal(url);
  } else {
    window.open(url, "_blank", "noopener,noreferrer");
  }
}
