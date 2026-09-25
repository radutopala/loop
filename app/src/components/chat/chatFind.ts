// Matches are ordered newest first, so stepping "next" walks up the chat to
// older messages. Both ends wrap, like every find bar: reaching the oldest
// match shouldn't be a dead end when the one you want is below it.
export function stepMatch(active: number, delta: number, count: number): number {
  if (count === 0) return 0;
  return (((active + delta) % count) + count) % count;
}

export function matchCountLabel(active: number, count: number): string {
  return count === 0 ? "no matches" : `${active + 1} / ${count}`;
}

export interface TextPoint {
  /** Index into the texts passed to locateMatch. */
  node: number;
  offset: number;
}

// locateMatch finds the first occurrence of term in a message's rendered
// text, given as its text nodes in document order. A match may span nodes
// ("agent<b>gate</b>"), so the texts are searched joined. Case is ignored,
// like the server's search. Returns null when the rendered text doesn't
// contain the term, e.g. when it's only in a link's URL.
export function locateMatch(texts: string[], term: string): { start: TextPoint; end: TextPoint } | null {
  if (term === "") return null;
  const at = texts.join("").toLowerCase().indexOf(term.toLowerCase());
  if (at < 0) return null;
  const point = (pos: number, isEnd: boolean): TextPoint => {
    let base = 0;
    for (let node = 0; node < texts.length; node++) {
      const len = texts[node]!.length;
      // An end exactly at a node's end stays in that node, not at the start of the next.
      if (pos < base + len || (isEnd && pos === base + len)) return { node, offset: pos - base };
      base += len;
    }
    return { node: texts.length - 1, offset: texts[texts.length - 1]!.length };
  };
  return { start: point(at, false), end: point(at + term.length, true) };
}
