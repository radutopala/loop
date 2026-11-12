import { useEffect, useState } from "react";
import { type FileSearchResult, type RootEntry, searchFiles } from "../../api/loopApi";

const DEBOUNCE_MS = 120;
const LIMIT = 50;

/** Searches the channel's roots for files whose path matches query, a
 * moment after the last keystroke. Null while the query is empty. */
export function useFileSearch(channelId: string, query: string): FileSearchResult[] | null {
  const [results, setResults] = useState<FileSearchResult[] | null>(null);
  const q = query.trim();
  useEffect(() => {
    if (!q) {
      setResults(null);
      return;
    }
    let live = true;
    const timer = window.setTimeout(() => {
      searchFiles(channelId, q, LIMIT, true)
        .then((r) => {
          if (live) setResults(r);
        })
        .catch(() => {
          if (live) setResults([]);
        });
    }, DEBOUNCE_MS);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [channelId, q]);
  return results;
}

/** A result's file name and where it lives: its directory, after the root's
 * name when the channel has more than one root. */
export function fileSearchLabel(result: FileSearchResult, roots: RootEntry[]): { name: string; where: string } {
  const slash = result.rel_path.lastIndexOf("/");
  const dir = slash >= 0 ? result.rel_path.slice(0, slash) : "";
  const root = roots.length > 1 ? roots.find((r) => r.index === result.root_index)?.name : undefined;
  return { name: result.name, where: [root, dir].filter(Boolean).join("/") };
}

/** The selection after a key in the results list, or null for another key. */
export function nextSearchIndex(key: string, index: number, count: number): number | null {
  if (count === 0) return null;
  if (key === "ArrowDown") return (index + 1) % count;
  if (key === "ArrowUp") return (index - 1 + count) % count;
  return null;
}
