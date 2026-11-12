import { useEffect, useState } from "react";
import { type GrepFile, type GrepOptions, type GrepResult, grepFiles } from "../../api/loopApi";

const DEBOUNCE_MS = 250;
/** The shortest query searched, as the daemon has it. */
export const MIN_CONTENT_QUERY = 2;

export type ContentSearch = { state: "idle" } | { state: "searching" } | { state: "done"; result: GrepResult } | { state: "error"; message: string };

/** Searches the contents of the channel's roots for query, a moment after
 * the last keystroke; idle while the query is too short or search is off. */
export function useContentSearch(channelId: string, query: string, opts: GrepOptions, enabled: boolean): ContentSearch {
  const [search, setSearch] = useState<ContentSearch>({ state: "idle" });
  const q = query.trim();
  const { regex, caseSensitive } = opts;
  useEffect(() => {
    if (!enabled || [...q].length < MIN_CONTENT_QUERY) {
      setSearch({ state: "idle" });
      return;
    }
    let live = true;
    setSearch((cur) => (cur.state === "done" ? cur : { state: "searching" }));
    const timer = window.setTimeout(() => {
      grepFiles(channelId, q, { regex, caseSensitive })
        .then((result) => {
          if (live) setSearch({ state: "done", result });
        })
        .catch((err: unknown) => {
          if (live) setSearch({ state: "error", message: err instanceof Error ? err.message : "Search failed" });
        });
    }, DEBOUNCE_MS);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [channelId, q, regex, caseSensitive, enabled]);
  return search;
}

/** One match row of the results: its file and match, in list order. */
export interface ContentHit {
  file: GrepFile;
  line: number;
}

/** The results' match rows in the order they show, for the keyboard. */
export function contentHits(files: GrepFile[]): ContentHit[] {
  return files.flatMap((file) => file.matches.map((m) => ({ file, line: m.line })));
}

/** text split around the match at 1-based character column, length
 * characters long, clamped to the text. */
export function highlightParts(text: string, column: number, length: number): [string, string, string] {
  const chars = [...text];
  const start = Math.min(Math.max(column - 1, 0), chars.length);
  const end = Math.min(start + Math.max(length, 0), chars.length);
  return [chars.slice(0, start).join(""), chars.slice(start, end).join(""), chars.slice(end).join("")];
}

/** The summary under the results: how many matches in how many files. */
export function contentSummary(result: GrepResult): string {
  const matches = result.files.reduce((n, f) => n + f.matches.length, 0);
  const files = result.files.length;
  const text = `${matches} ${matches === 1 ? "match" : "matches"} in ${files} ${files === 1 ? "file" : "files"}`;
  return result.truncated ? `${text}; more not shown, refine the search` : text;
}
