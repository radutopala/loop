// File-path detection and async validation for chat messages.
//
// `findCandidatePaths` extracts plausible file paths from a string. Validation
// happens via `validatePaths`, which batches HEAD-style checks against the loop
// fs and caches the answers per channel. Subscribers (e.g. <FileLink/>) call
// `getValidationStatus` to render conditionally and `subscribe` to update once
// the result lands.

import { checkFilesExist } from "../api/files";

export interface FileLinkTarget {
  rootIndex: number;
  relPath: string;
}

export interface ParsedCandidate {
  /** The raw path token as it appears in the source text, without any line suffix. */
  raw: string;
  /** Optional 1-based line number parsed from a trailing `:N` suffix. */
  line: number | null;
  /** Position of `raw` in the original input string. */
  start: number;
  /** Length of the entire matched token (including the optional `:N` suffix). */
  length: number;
}

// File path heuristic. Matches:
//   - relative paths with at least one `/` and a file extension (src/foo.ts)
//   - absolute paths starting with `/` (e.g. /Users/.../file.go)
// Optional `:NNN` line suffix is captured separately so callers can jump.
//
// Notes:
//   - Two-segment minimum prevents matching common URLs ("foo.com")
//   - Limit chars to filename-safe set (no spaces / quotes / parens)
//   - Trailing punctuation (".", ",", ")", "]", "'", '"', ";", ":") is excluded
const PATH_REGEX = /(?<![\w./])((?:\/|(?:[\w.\-+@]+\/)+)[\w.\-+@]+(?:\/[\w.\-+@]+)*\.[a-zA-Z0-9]{1,8})(?::(\d+))?(?![\w/])/g;

export function findCandidatePaths(text: string): ParsedCandidate[] {
  const out: ParsedCandidate[] = [];
  if (!text) return out;
  PATH_REGEX.lastIndex = 0;
  for (;;) {
    const m = PATH_REGEX.exec(text);
    if (!m) break;
    const raw = m[1] ?? "";
    if (!raw) continue;
    if (looksLikeUrl(text, m.index)) continue;
    const lineStr = m[2];
    out.push({
      raw,
      line: lineStr ? parseInt(lineStr, 10) : null,
      start: m.index,
      length: m[0].length,
    });
  }
  return out;
}

// A `cd <dir>` starting a shell command: at the start, after a newline, or
// after `&&`, `||`, `;` or `|`. The dir may be quoted.
const CD_REGEX = /(?:^|[\n;&|(])\s*cd\s+(?:"([^"]+)"|'([^']+)'|([^\s;&|)]+))/g;

/** Where each `cd` to an absolute directory in a shell command takes it,
 * by the offset the `cd` ends at; a `cd` to a relative directory moves on
 * from the one before it, when there is one. */
export function commandDirs(command: string): { end: number; dir: string }[] {
  const out: { end: number; dir: string }[] = [];
  CD_REGEX.lastIndex = 0;
  for (;;) {
    const m = CD_REGEX.exec(command);
    if (!m) break;
    const arg = (m[1] ?? m[2] ?? m[3] ?? "").replace(/\/+$/, "");
    const prev = out[out.length - 1]?.dir;
    const dir = arg.startsWith("/") ? arg : prev && arg && !arg.startsWith("~") && !arg.startsWith("$") ? joinPath(prev, arg) : null;
    if (dir) out.push({ end: m.index + m[0].length, dir });
  }
  return out;
}

/** The path to check for raw: as written when absolute or no `cd` comes
 * before it, else joined to the dir the last `cd` before it moved to. */
export function pathInDir(raw: string, start: number, dirs: { end: number; dir: string }[]): string {
  if (raw.startsWith("/")) return raw;
  let dir: string | null = null;
  for (const d of dirs) if (d.end <= start) dir = d.dir;
  return dir ? joinPath(dir, raw) : raw;
}

function joinPath(dir: string, rel: string): string {
  const parts = dir.split("/");
  for (const seg of rel.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") {
      if (parts.length > 1) parts.pop();
    } else parts.push(seg);
  }
  return parts.join("/") || "/";
}

// Skip http(s)://example.com/file.ext and similar URL patterns.
function looksLikeUrl(text: string, idx: number): boolean {
  if (idx < 3) return false;
  const before = text.slice(Math.max(0, idx - 8), idx);
  return /https?:\/\/$/.test(before) || /file:\/\/$/.test(before);
}

// ── Per-channel validation cache ──

type Status = { kind: "unknown" } | { kind: "pending" } | { kind: "valid"; target: FileLinkTarget } | { kind: "invalid" };

interface ChannelCache {
  statuses: Map<string, Status>;
  // How many links on screen show each path.
  shown: Map<string, number>;
  pending: Set<string>;
  flushTimer: ReturnType<typeof setTimeout> | null;
  listeners: Set<() => void>;
}

const channelCaches = new Map<string, ChannelCache>();
const FLUSH_DELAY_MS = 50;
const BATCH_LIMIT = 100;

function getCache(channelId: string): ChannelCache {
  let c = channelCaches.get(channelId);
  if (!c) {
    c = { statuses: new Map(), shown: new Map(), pending: new Set(), flushTimer: null, listeners: new Set() };
    channelCaches.set(channelId, c);
  }
  return c;
}

export function getValidationStatus(channelId: string, raw: string): Status {
  const c = getCache(channelId);
  return c.statuses.get(raw) ?? { kind: "unknown" };
}

export function subscribe(channelId: string, listener: () => void): () => void {
  const c = getCache(channelId);
  c.listeners.add(listener);
  return () => c.listeners.delete(listener);
}

function notify(c: ChannelCache) {
  for (const l of c.listeners) l();
}

export function requestValidation(channelId: string, raw: string): void {
  const c = getCache(channelId);
  if (c.statuses.has(raw)) return;
  c.statuses.set(raw, { kind: "pending" });
  c.pending.add(raw);
  if (c.flushTimer) return;
  c.flushTimer = setTimeout(() => flush(channelId), FLUSH_DELAY_MS);
}

/** Marks raw as shown by a link on screen, until the returned release
 * runs; revalidateMissing checks only paths that are shown. */
export function retain(channelId: string, raw: string): () => void {
  const c = getCache(channelId);
  c.shown.set(raw, (c.shown.get(raw) ?? 0) + 1);
  return () => {
    const n = (c.shown.get(raw) ?? 1) - 1;
    if (n > 0) c.shown.set(raw, n);
    else c.shown.delete(raw);
  };
}

/** Checks again the shown paths that weren't found: a tool that just ran
 * may have made them. */
export function revalidateMissing(channelId: string): void {
  const c = channelCaches.get(channelId);
  if (!c) return;
  for (const raw of c.shown.keys()) {
    if (c.statuses.get(raw)?.kind !== "invalid") continue;
    c.statuses.delete(raw);
    requestValidation(channelId, raw);
  }
}

async function flush(channelId: string) {
  const c = getCache(channelId);
  c.flushTimer = null;
  if (c.pending.size === 0) return;

  const batch = Array.from(c.pending).slice(0, BATCH_LIMIT);
  for (const p of batch) c.pending.delete(p);

  try {
    const results = await checkFilesExist(channelId, batch);
    const byPath = new Map(results.map((r) => [r.path, r]));
    for (const p of batch) {
      const r = byPath.get(p);
      if (r && r.exists && r.rel_path !== undefined && r.root_index !== undefined) {
        c.statuses.set(p, { kind: "valid", target: { rootIndex: r.root_index, relPath: r.rel_path } });
      } else {
        c.statuses.set(p, { kind: "invalid" });
      }
    }
  } catch {
    for (const p of batch) c.statuses.set(p, { kind: "invalid" });
  }
  notify(c);

  if (c.pending.size > 0) {
    c.flushTimer = setTimeout(() => flush(channelId), FLUSH_DELAY_MS);
  }
}

// Test/dev hook to drop cache state for a specific channel.
export function resetFileLinkCache(channelId?: string): void {
  if (channelId === undefined) {
    channelCaches.clear();
    return;
  }
  channelCaches.delete(channelId);
}
