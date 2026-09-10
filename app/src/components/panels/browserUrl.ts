/**
 * Schemes the URL bar hands to Chrome untouched.
 *
 * Anything not listed here is treated as a bare host — "example.com",
 * "localhost:3000" — and gets an https:// prefix, which is what the bar has
 * always done. The list matters because a generic `scheme:` test would read
 * "localhost:3000" as the "localhost" scheme and stop prefixing it.
 */
const KNOWN_SCHEME = /^(https?|chrome|chrome-extension|devtools|about|file|view-source|data|blob):/i;

/**
 * normalizeNavigateUrl turns what the user typed into a URL Chrome can open,
 * or null when there is nothing to navigate to.
 *
 * Internal pages are the reason this isn't a one-liner: the bar used to prefix
 * everything without an http(s) scheme, so "chrome://extensions/" was requested
 * as "https://chrome://extensions/" and silently failed to load.
 */
export function normalizeNavigateUrl(input: string): string | null {
  const trimmed = input.trim();
  if (!trimmed) return null;
  if (KNOWN_SCHEME.test(trimmed)) return trimmed;
  return "https://" + trimmed;
}

/**
 * The one icon form the tab strip accepts: a base64 raster the daemon inlined.
 *
 * The six types are exactly what the daemon's content sniff can name, spelled
 * the way it spells them. SVG is absent because the sniff never reports it —
 * markup is not something a 12px icon needs to be.
 */
const SAFE_ICON_URL = /^data:image\/(?:png|jpeg|gif|webp|bmp|x-icon);base64,[A-Za-z0-9+/=]+$/;

/**
 * safeFaviconUrl returns the icon only when the pane may render it, and
 * undefined otherwise, which leaves the strip drawing its plain dot.
 *
 * Tab icons arrive already inlined: the daemon pulls the bytes through the
 * sidecar's own network and hands the pane a data: URL. A remote URL reaching
 * here would mean the page named a host and the renderer fetched it from the
 * user's machine, so anything that is not an inline raster is dropped rather
 * than loaded.
 */
export function safeFaviconUrl(url: string | undefined): string | undefined {
  if (!url) return undefined;
  const candidate = url.trim();
  if (!candidate.startsWith("data:image/")) return undefined;
  if (!SAFE_ICON_URL.test(candidate)) return undefined;
  return candidate;
}
