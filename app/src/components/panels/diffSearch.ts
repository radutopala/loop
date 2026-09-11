import type { DiffFile } from "../../api/loopApi";
import type { ParsedFile } from "./DiffViewer";

// ── Fuzzy path matching ───────────────────────────────────────────────────
//
// A port of fzf's FuzzyMatchV1: scan forward for the first subsequence match
// to find where it ends, scan backward from there to find the tightest start,
// then score only that region. Two passes over the text, no matrix — which is
// why fzf uses it for the long tail of candidates.
//
// The scoring is the point. A plain subsequence test (which is all the command
// palette does today) can tell you "dv" is in "DiffViewer.tsx", but not that
// it should outrank a file that merely happens to contain a d before a v.
// These bonuses are what encode "matched the start of a path segment" and
// "matched a camelCase hump" as better than "matched mid-word".

const SCORE_MATCH = 16;
const SCORE_GAP_START = -3;
const SCORE_GAP_EXTENSION = -1;
/** First character of a word — after a separator, or at the very start. */
const BONUS_BOUNDARY = SCORE_MATCH / 2;
/** A camelCase hump, or a digit following a letter. */
const BONUS_CAMEL = BONUS_BOUNDARY + SCORE_GAP_EXTENSION;
/** Cancels out a gap, so an unbroken run always beats a split one. */
const BONUS_CONSECUTIVE = -(SCORE_GAP_START + SCORE_GAP_EXTENSION);
/** Matching the first character at all is worth extra. */
const BONUS_FIRST_CHAR_MULTIPLIER = 2;

enum CharClass {
  NonWord = 0,
  Lower = 1,
  Upper = 2,
  Digit = 3,
}

function classOf(ch: string): CharClass {
  if (ch >= "a" && ch <= "z") return CharClass.Lower;
  if (ch >= "A" && ch <= "Z") return CharClass.Upper;
  if (ch >= "0" && ch <= "9") return CharClass.Digit;
  return CharClass.NonWord;
}

function bonusFor(prev: CharClass, cur: CharClass): number {
  if (cur !== CharClass.NonWord && prev === CharClass.NonWord) return BONUS_BOUNDARY;
  if ((prev === CharClass.Lower && cur === CharClass.Upper) || (prev !== CharClass.Digit && cur === CharClass.Digit)) return BONUS_CAMEL;
  return 0;
}

export interface FuzzyResult {
  score: number;
  /** Indices into `text` that the query matched, ascending. Drives highlighting. */
  positions: number[];
}

/**
 * Score `query` against `text`, or null when it isn't a subsequence.
 *
 * Smart case, the same rule fzf and most editors use: an all-lowercase query
 * matches case-insensitively, a query with any uppercase is case-sensitive.
 * Typing `readme` should find `README.md`, but typing `Diff` shouldn't drag in
 * every `diff`.
 */
export function fuzzyScore(query: string, text: string): FuzzyResult | null {
  if (query === "") return { score: 0, positions: [] };
  const caseSensitive = query !== query.toLowerCase();
  const hay = caseSensitive ? text : text.toLowerCase();
  const needle = caseSensitive ? query : query.toLowerCase();

  // Forward pass — where does the first possible match end?
  let qi = 0;
  let endIdx = -1;
  for (let ti = 0; ti < hay.length; ti++) {
    if (hay[ti] === needle[qi]) {
      qi++;
      if (qi === needle.length) {
        endIdx = ti;
        break;
      }
    }
  }
  if (endIdx < 0) return null;

  // Backward pass — within the region the forward pass ended on, pull the
  // start as far right as it will go, so "vt" against "viewer/view.ts" scores
  // the tight v-to-t at the end rather than spanning from index 0. Note this
  // tightens the start only: V1 stops at the first subsequence it finds and
  // does not go looking for a better one later in the string. That is the
  // trade that makes it two passes instead of a matrix, and it is fine for
  // ranking the handful of paths in a diff.
  qi = needle.length - 1;
  let startIdx = 0;
  for (let ti = endIdx; ti >= 0; ti--) {
    if (hay[ti] === needle[qi]) {
      qi--;
      if (qi < 0) {
        startIdx = ti;
        break;
      }
    }
  }

  // Score the chosen region.
  let score = 0;
  let inGap = false;
  let consecutive = 0;
  let firstBonus = 0;
  const positions: number[] = [];
  qi = 0;
  for (let ti = startIdx; ti <= endIdx; ti++) {
    if (hay[ti] === needle[qi]) {
      positions.push(ti);
      score += SCORE_MATCH;
      const prev = ti === 0 ? CharClass.NonWord : classOf(text[ti - 1] as string);
      let bonus = bonusFor(prev, classOf(text[ti] as string));
      if (consecutive === 0) {
        firstBonus = bonus;
      } else {
        // A run keeps the best bonus any of its members earned, so the run is
        // rewarded as a unit rather than decaying after its first character.
        if (bonus >= BONUS_BOUNDARY && bonus > firstBonus) firstBonus = bonus;
        bonus = Math.max(bonus, Math.max(firstBonus, BONUS_CONSECUTIVE));
      }
      score += qi === 0 ? bonus * BONUS_FIRST_CHAR_MULTIPLIER : bonus;
      inGap = false;
      consecutive++;
      qi++;
      if (qi === needle.length) break;
    } else {
      score += inGap ? SCORE_GAP_EXTENSION : SCORE_GAP_START;
      inGap = true;
      consecutive = 0;
      firstBonus = 0;
    }
  }
  return { score, positions };
}

export interface PathMatch {
  /** Index into the `files` array the DiffViewer renders. */
  fileIndex: number;
  path: string;
  score: number;
  positions: number[];
}

/**
 * Rank diff files by how well their path matches `query`, best first.
 * Ties break on file order so the list doesn't reshuffle between keystrokes.
 */
export function searchPaths(files: Pick<DiffFile, "path">[], query: string): PathMatch[] {
  const q = query.trim();
  if (q === "") return [];
  const out: PathMatch[] = [];
  for (let i = 0; i < files.length; i++) {
    const path = files[i]?.path ?? "";
    const res = fuzzyScore(q, path);
    if (res) out.push({ fileIndex: i, path, score: res.score, positions: res.positions });
  }
  out.sort((a, b) => b.score - a.score || a.fileIndex - b.fileIndex);
  return out;
}

// ── Content matching ──────────────────────────────────────────────────────

export interface ContentMatch {
  /** Index into the `files` array the DiffViewer renders. */
  fileIndex: number;
  hunkIndex: number;
  lineIndex: number;
  /** Offsets into the line's content. */
  start: number;
  end: number;
}

/** Stable address for a rendered hunk line — also the DOM lookup key. */
export function lineAddr(fileIndex: number, hunkIndex: number, lineIndex: number): string {
  return `${fileIndex}:${hunkIndex}:${lineIndex}`;
}

/**
 * Every match within one line, left to right. Empty for an empty needle.
 *
 * Literal substring, smart case — the same rule `fuzzyScore` uses, so one box
 * does not change its mind about case depending on which mode you are in.
 * One needle against one haystack is exactly the case where a scan beats any
 * automaton: Aho-Corasick earns its keep on many patterns at once, which a find
 * bar never has.
 */
export function matchesInLine(content: string, query: string): Array<{ start: number; end: number }> {
  if (query === "") return [];
  const caseSensitive = query !== query.toLowerCase();
  const hay = caseSensitive ? content : content.toLowerCase();
  const needle = caseSensitive ? query : query.toLowerCase();
  const out: Array<{ start: number; end: number }> = [];
  let from = 0;
  for (;;) {
    const at = hay.indexOf(needle, from);
    if (at < 0) break;
    out.push({ start: at, end: at + needle.length });
    from = at + needle.length;
  }
  return out;
}

/**
 * All content matches across the diff, in render order, so stepping through
 * them walks the view top to bottom.
 *
 * Only hunk lines are searched. Context revealed by expanding a gap is fetched
 * on demand and isn't part of the diff, so including it would make the match
 * count depend on which gaps happen to be open.
 */
export function searchContent(files: Array<Pick<DiffFile, "path" | "status">>, parsedFiles: ParsedFile[], query: string): ContentMatch[] {
  const q = query.trim();
  if (q === "") return [];
  const out: ContentMatch[] = [];
  for (let fileIndex = 0; fileIndex < files.length; fileIndex++) {
    const file = files[fileIndex];
    if (!file) continue;
    // Same pairing rule the renderer uses, so fileIndex addresses the same row.
    const parsed = parsedFiles.find((pf) => pf.path === file.path && pf.status === file.status);
    if (!parsed) continue;
    for (let hunkIndex = 0; hunkIndex < parsed.hunks.length; hunkIndex++) {
      const lines = parsed.hunks[hunkIndex]?.lines ?? [];
      for (let lineIndex = 0; lineIndex < lines.length; lineIndex++) {
        for (const m of matchesInLine(lines[lineIndex]?.content ?? "", q)) {
          out.push({ fileIndex, hunkIndex, lineIndex, start: m.start, end: m.end });
        }
      }
    }
  }
  return out;
}

export type SearchMode = "content" | "path";

/**
 * Split the raw input into a mode and a term. A leading ">" switches to path
 * jumping, mirroring the convention command palettes use for "this is a
 * command, not a search".
 */
export function parseQuery(raw: string): { mode: SearchMode; term: string } {
  if (raw.startsWith(">")) return { mode: "path", term: raw.slice(1).trim() };
  return { mode: "content", term: raw };
}
