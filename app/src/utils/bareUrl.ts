/**
 * A bare URL in chat text, as a regex source. It runs to the next space,
 * angle bracket, double quote or backtick, so it takes in the `'`, `(`, `)`
 * and `*` some consoles' links are full of; trimUrl then drops what follows
 * the URL in prose.
 */
export const BARE_URL = '(?:https?:\\/\\/|loop:\\/\\/channel\\/)[^\\s<>"`]+';

// What prose puts right after a URL, and a URL rarely ends with.
const TRAILING = new Set([".", ",", ":", ";", "!", "?", "'", "*", "_", "~"]);

const count = (s: string, ch: string) => s.split(ch).length - 1;

/**
 * trimUrl drops the end of a BARE_URL match that belongs to the sentence:
 * trailing punctuation, and a `)` the URL opened no `(` for, as in
 * "(see https://example.test/x)".
 */
export function trimUrl(raw: string): string {
  let url = raw;
  for (;;) {
    const last = url.at(-1) ?? "";
    if (TRAILING.has(last) || (last === ")" && count(url, "(") < count(url, ")"))) {
      url = url.slice(0, -1);
      continue;
    }
    return url;
  }
}
