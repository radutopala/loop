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
