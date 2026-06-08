import { gutter, GutterMarker } from "@codemirror/view";
import { StateField, StateEffect } from "@codemirror/state";
import { parseUnifiedDiff, type ParsedFile } from "./DiffViewer";

// JetBrains/GoLand-style VCS change markers in the editor gutter: a thin
// coloured bar to the left of each line that has been added or modified
// relative to git HEAD, plus a small triangle where lines were deleted.
//
// The data comes from the channel's unified diff (the same patch the Git
// panel renders); colours live in editorTheme.ts so they reconfigure with the
// active palette.

export type GitChangeKind = "added" | "modified";

/** Per-line VCS change classification for the currently-open file, keyed by
 * 1-based new-file line number, plus the lines that carry a deletion marker. */
export interface GitLineChanges {
  /** new-file line number -> "added" | "modified" */
  changed: Map<number, GitChangeKind>;
  /** new-file line numbers that have deleted lines immediately above them */
  deletedAbove: Set<number>;
  /** true when the deletion is at the very end of the file (no line below) */
  deletedAtEnd: boolean;
}

export function emptyGitLineChanges(): GitLineChanges {
  return { changed: new Map(), deletedAbove: new Set(), deletedAtEnd: false };
}

export function hasGitLineChanges(c: GitLineChanges): boolean {
  return c.changed.size > 0 || c.deletedAbove.size > 0 || c.deletedAtEnd;
}

/** Single source of truth for the VCS bar colours, shared by the editor-gutter
 * CSS (editorTheme.ts) and the right-side overview ruler. */
export function gitGutterColors(isDark: boolean) {
  return {
    added: isDark ? "#62b543" : "#59a869",
    modified: isDark ? "#3592c4" : "#3574f0",
    deleted: isDark ? "#868a91" : "#9aa7b0",
  };
}

/** A mark on the right-side overview ruler, positioned as a fraction of the
 * whole document so the ruler reads as a bird's-eye map of all changes. */
export interface OverviewMark {
  topFrac: number;
  heightFrac: number;
  kind: GitChangeKind | "deleted";
  /** 1-based line to jump to when the mark is clicked. */
  line: number;
}

/** Collapse line changes into proportional overview-ruler marks, coalescing
 * runs of consecutive same-kind lines into a single block. */
export function gitOverviewMarks(changes: GitLineChanges, totalLines: number): OverviewMark[] {
  if (totalLines <= 0) return [];
  const marks: OverviewMark[] = [];

  const entries = [...changes.changed.entries()].sort((a, b) => a[0] - b[0]);
  let i = 0;
  while (i < entries.length) {
    const [startLine, kind] = entries[i]!;
    let end = startLine;
    let j = i + 1;
    while (j < entries.length && entries[j]![0] === end + 1 && entries[j]![1] === kind) {
      end = entries[j]![0];
      j++;
    }
    marks.push({
      topFrac: (startLine - 1) / totalLines,
      heightFrac: (end - startLine + 1) / totalLines,
      kind,
      line: startLine,
    });
    i = j;
  }

  for (const line of changes.deletedAbove) {
    marks.push({ topFrac: (line - 1) / totalLines, heightFrac: 0, kind: "deleted", line });
  }
  if (changes.deletedAtEnd) {
    marks.push({ topFrac: (totalLines - 1) / totalLines, heightFrac: 0, kind: "deleted", line: totalLines });
  }

  return marks;
}

/** Find the next new-file line number at or after index `from` in a hunk. */
function nextNewNum(lines: ParsedFile["hunks"][number]["lines"], from: number): number | null {
  for (let i = from; i < lines.length; i++) {
    const n = lines[i]!.newNum;
    if (n !== null) return n;
  }
  return null;
}

/**
 * Collapse a file's diff hunks into per-line markers. A maximal run of
 * non-context lines is one "change block":
 *   - additions only            -> "added" (green) on each new line
 *   - deletions + additions      -> "modified" (blue) on each new line
 *   - deletions only             -> a deletion triangle anchored to the line
 *                                   that now sits below the removed region
 *                                   (or deletedAtEnd when removed at EOF)
 * Accepts the matching ParsedFile entries (a path can appear twice when it is
 * partially staged) and merges them.
 */
export function computeGitLineChanges(parsedFiles: ParsedFile[]): GitLineChanges {
  const result = emptyGitLineChanges();

  for (const parsed of parsedFiles) {
    for (const hunk of parsed.hunks) {
      const lines = hunk.lines;
      let i = 0;
      while (i < lines.length) {
        if (lines[i]!.type === "ctx") {
          i++;
          continue;
        }
        let dels = 0;
        const adds: number[] = [];
        while (i < lines.length && lines[i]!.type !== "ctx") {
          const line = lines[i]!;
          if (line.type === "del") {
            dels++;
          } else if (line.type === "add" && line.newNum !== null) {
            adds.push(line.newNum);
          }
          i++;
        }
        if (adds.length > 0) {
          const kind: GitChangeKind = dels > 0 ? "modified" : "added";
          for (const n of adds) result.changed.set(n, kind);
        } else if (dels > 0) {
          // Pure deletion: anchor a triangle to the line below the removed
          // region (the context/added line that follows in the hunk).
          const below = nextNewNum(lines, i);
          if (below !== null) result.deletedAbove.add(below);
          else result.deletedAtEnd = true;
        }
      }
    }
  }

  return result;
}

/**
 * Parse the channel's combined unified diff and extract the change markers for
 * one file. `relPath` is the file path relative to its repo root, matching the
 * paths git emits in the patch.
 */
export function gitLineChangesForFile(combinedDiff: string, relPath: string): GitLineChanges {
  if (!combinedDiff || !relPath) return emptyGitLineChanges();
  const matching = parseUnifiedDiff(combinedDiff).filter((f) => f.path === relPath);
  return computeGitLineChanges(matching);
}

// ── CodeMirror extension ──

class ChangeBarMarker extends GutterMarker {
  constructor(readonly kind: GitChangeKind | "deleted") {
    super();
  }
  eq(other: ChangeBarMarker) {
    return other.kind === this.kind;
  }
  toDOM() {
    const el = document.createElement("div");
    el.className = `cm-gitChange cm-gitChange-${this.kind}`;
    return el;
  }
}

const addedMarker = new ChangeBarMarker("added");
const modifiedMarker = new ChangeBarMarker("modified");
const deletedMarker = new ChangeBarMarker("deleted");

/** Effect that swaps in a fresh set of line changes for the open file. */
export const setGitLineChanges = StateEffect.define<GitLineChanges>();

/** Holds the current file's VCS line changes; updated via setGitLineChanges. */
export const gitLineChangesField = StateField.define<GitLineChanges>({
  create: emptyGitLineChanges,
  update(value, tr) {
    for (const effect of tr.effects) {
      if (effect.is(setGitLineChanges)) return effect.value;
    }
    return value;
  },
});

/** The gutter column rendering the change bars + deletion triangles. */
export const gitChangeGutter = gutter({
  class: "cm-gitChangeGutter",
  lineMarker(view, line) {
    const changes = view.state.field(gitLineChangesField, false);
    if (!changes) return null;
    const lineNo = view.state.doc.lineAt(line.from).number;
    const kind = changes.changed.get(lineNo);
    if (kind) return kind === "added" ? addedMarker : modifiedMarker;
    if (changes.deletedAbove.has(lineNo)) return deletedMarker;
    if (changes.deletedAtEnd && lineNo === view.state.doc.lines) return deletedMarker;
    return null;
  },
  lineMarkerChange(update) {
    return update.startState.field(gitLineChangesField, false) !== update.state.field(gitLineChangesField, false);
  },
});

/** Full extension: the state field + the gutter column. */
export const gitChangeGutterExtension = [gitLineChangesField, gitChangeGutter];
