import type { DiffFileStatus } from "../../api/loopApi";

export interface ParsedHunk {
  header: string;
  lines: HunkLine[];
}

export interface HunkLine {
  type: "add" | "del" | "ctx";
  content: string;
  oldNum: number | null;
  newNum: number | null;
}

export interface ParsedFile {
  path: string;
  hunks: ParsedHunk[];
  // Set when the diff was parsed in uncommitted mode so the file list can
  // disambiguate a partially-staged path that appears twice (once staged,
  // once unstaged). Undefined for branch-to-branch diffs where the
  // distinction does not apply.
  status?: DiffFileStatus;
}
