import { escapeHTML } from "./markdownTable";

// The copy flavors of a fenced code block in a chat message, beside its plain
// text: the fence itself, for anywhere that speaks markdown (Slack, Teams,
// GitHub), and a <pre><code> block, for rich-text targets (Teams, docs, mail)
// that keep a code block's monospace and line breaks only from HTML.

/**
 * codeToFence wraps a code block's text back in a fence, with more backticks
 * than any run of them inside it so the body can't close it early.
 */
export function codeToFence(body: string, info: string): string {
  let longest = 0;
  for (const run of body.match(/`+/g) ?? []) longest = Math.max(longest, run.length);
  const ticks = "`".repeat(Math.max(3, longest + 1));
  return `${ticks}${info}\n${body}\n${ticks}`;
}

/** codeToHTML renders a code block's text as an HTML <pre><code> block. */
export function codeToHTML(body: string, info: string): string {
  // The info string's first word is the language, as in CommonMark.
  const lang = info.split(/\s+/)[0] ?? "";
  const cls = lang ? ` class="language-${escapeHTML(lang)}"` : "";
  return `<pre><code${cls}>${escapeHTML(body)}</code></pre>`;
}
