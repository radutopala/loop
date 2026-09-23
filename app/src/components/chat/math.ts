import katex from "katex";

// LaTeX math in chat messages, rendered with KaTeX. Agents write display math
// as $$…$$ or \[…\] (on one line or spread over several) and inline math as
// $…$ or \(…\).

/** A display-math block, and the index of the first line after it. */
export type MathBlock = { tex: string; source: string; next: number };

const blockDelims: [string, string][] = [
  ["$$", "$$"],
  ["\\[", "\\]"],
];

/**
 * findMathBlock reads a display-math block starting at lines[i]: a line that
 * opens with $$ or \[, up to the line that closes it. An opener that's never
 * closed isn't math, so it returns null and the line stays text.
 */
export function findMathBlock(lines: string[], i: number): MathBlock | null {
  const first = (lines[i] ?? "").trim();
  for (const [open, close] of blockDelims) {
    if (!first.startsWith(open)) continue;
    const rest = first.slice(open.length);
    if (rest.trim().endsWith(close)) {
      const tex = rest.trim().slice(0, -close.length).trim();
      // "$$a$$ and $$b$$" is two inline formulas, not one block.
      return tex && !tex.includes(open) ? { tex, source: first, next: i + 1 } : null;
    }
    const body = [rest];
    for (let j = i + 1; j < lines.length; j++) {
      const line = (lines[j] ?? "").trim();
      if (line.endsWith(close)) {
        body.push(line.slice(0, -close.length));
        const tex = body.join("\n").trim();
        return tex ? { tex, source: lines.slice(i, j + 1).join("\n"), next: j + 1 } : null;
      }
      body.push(line);
    }
    return null;
  }
  return null;
}

/**
 * inlineMathPattern matches inline math: $$…$$ and \(…\) anywhere, and $…$
 * under pandoc's rules so prose with dollar amounts stays prose: the opening
 * $ is followed by a non-space, the closing $ follows a non-space and isn't
 * followed by a digit or letter ("costs $5 and $10", "$HOME/$USER").
 */
export const inlineMathPattern = String.raw`\$\$[^$\n]+?\$\$|\\\(.+?\\\)|(?<![\\$\w])\$(?![\s$])(?:[^$\n\\]|\\.)+?(?<![\s\\])\$(?![\w$])`;

/** inlineMathTeX strips the delimiters from an inlineMathPattern match. */
export function inlineMathTeX(token: string): string {
  if (token.startsWith("$$")) return token.slice(2, -2).trim();
  if (token.startsWith("\\(")) return token.slice(2, -2).trim();
  return token.slice(1, -1);
}

/** isInlineMath reports whether a token matched by inlineMathPattern is math. */
export function isInlineMath(token: string): boolean {
  return token.startsWith("$") || token.startsWith("\\(");
}

// Messages re-render on every streamed chunk, so rendered formulas are cached.
// Keyed by mode and source; bounded so a long session doesn't grow it forever.
const cache = new Map<string, string>();
const cacheLimit = 500;

/**
 * renderMath renders LaTeX to HTML. Bad input doesn't throw: KaTeX shows the
 * source in red at the error, so the rest of the message still renders.
 * trust stays off, so \href and friends can't inject links or HTML.
 */
export function renderMath(tex: string, displayMode: boolean): string {
  const key = `${displayMode ? "D" : "I"}${tex}`;
  const hit = cache.get(key);
  if (hit !== undefined) return hit;
  const html = katex.renderToString(tex, { displayMode, throwOnError: false, trust: false });
  if (cache.size >= cacheLimit) cache.delete(cache.keys().next().value as string);
  cache.set(key, html);
  return html;
}
