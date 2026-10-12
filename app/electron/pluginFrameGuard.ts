/** A plugin's panels and cards load through a content link, `/c/<cap>/`,
 *  whose cap is base64url JSON with the link's kind in `k`, then `.` and a
 *  signature. pluginFrameBase returns the `/c/<cap>/` base of url when it is
 *  a plugin's link, else "". */
export function pluginFrameBase(url: string): string {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return "";
  }
  const cap = /^\/c\/([^/]+)\//.exec(u.pathname)?.[1];
  if (!cap) return "";
  const payload = cap.split(".")[0] ?? "";
  try {
    const json = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
    if ((JSON.parse(json) as { k?: unknown }).k !== "plugin") return "";
  } catch {
    return "";
  }
  return `${u.origin}/c/${cap}/`;
}

/** Whether a frame showing frameURL may go to targetURL when the frame
 *  itself started it. A plugin frame stays inside its own bundle: a link or
 *  `location = …` to anything else is refused, since CSP doesn't cover a
 *  frame navigating itself. The app pointing a frame at a page (a new link
 *  after the old one expired) isn't the frame's own navigation, so it isn't
 *  checked here. Other frames are left alone. */
export function allowFrameNavigation(frameURL: string, targetURL: string): boolean {
  const base = pluginFrameBase(frameURL);
  if (!base) return true;
  return targetURL.startsWith(base);
}
