import { useCallback, useEffect, useRef, useState } from "react";
import type { DiffFile, DiffFileStatus } from "../../api/loopApi";
import { fetchFileContent } from "../../api/loopApi";
import { fonts } from "../../theme";
import type { ColorPalette } from "../../theme";
import { useTheme } from "../../ThemeContext";

// ── Types ──

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

/** Stable identifier that pairs a file with its status — used as the key
 * for expandedFiles and to look up the matching ParsedFile. Two rows for
 * the same path with different statuses get distinct keys. */
export function fileKey(file: { path: string; status?: DiffFileStatus }): string {
  return `${file.status ?? ""}:${file.path}`;
}

export interface ExpandableGap {
  startLine: number;   // 1-based new-file line where gap starts
  endLine: number;     // 1-based new-file line where gap ends (inclusive)
  oldStart: number;    // corresponding old-file line number
  totalLines: number;
}

export type DiffSegment =
  | { kind: "hunk"; hunk: ParsedHunk; hunkIndex: number }
  | { kind: "gap"; gap: ExpandableGap; position: "top" | "middle" | "bottom" };

// ── Helper functions ──

/** Format a rename as "prefix/{old => new}/suffix" like git does. */
export function formatRenamePath(oldPath: string, newPath: string): string {
  // Find common prefix.
  const oldParts = oldPath.split("/");
  const newParts = newPath.split("/");
  let prefixLen = 0;
  while (prefixLen < oldParts.length && prefixLen < newParts.length && oldParts[prefixLen] === newParts[prefixLen]) {
    prefixLen++;
  }
  // Find common suffix.
  let suffixLen = 0;
  while (suffixLen < oldParts.length - prefixLen && suffixLen < newParts.length - prefixLen && oldParts[oldParts.length - 1 - suffixLen] === newParts[newParts.length - 1 - suffixLen]) {
    suffixLen++;
  }
  const prefix = oldParts.slice(0, prefixLen).join("/");
  const suffix = oldParts.slice(oldParts.length - suffixLen).join("/");
  const oldMiddle = oldParts.slice(prefixLen, oldParts.length - suffixLen).join("/");
  const newMiddle = newParts.slice(prefixLen, newParts.length - suffixLen).join("/");

  const parts: string[] = [];
  if (prefix) parts.push(prefix + "/");
  parts.push(`{${oldMiddle} => ${newMiddle}}`);
  if (suffix) parts.push("/" + suffix);
  return parts.join("");
}

/** Return the first and last new-file line number in a hunk. */
function hunkNewRange(hunk: ParsedHunk): { first: number; last: number } {
  let first = Infinity, last = 0;
  for (const line of hunk.lines) {
    if (line.newNum !== null) {
      if (line.newNum < first) first = line.newNum;
      if (line.newNum > last) last = line.newNum;
    }
  }
  return { first, last };
}

/** Return the first and last old-file line number in a hunk. */
function hunkOldRange(hunk: ParsedHunk): { first: number; last: number } {
  let first = Infinity, last = 0;
  for (const line of hunk.lines) {
    if (line.oldNum !== null) {
      if (line.oldNum < first) first = line.oldNum;
      if (line.oldNum > last) last = line.oldNum;
    }
  }
  return { first, last };
}

/** Check if a parsed file is new/untracked (all additions, no old content). */
function isNewFile(parsed: ParsedFile): boolean {
  if (parsed.hunks.length === 0) return false;
  return parsed.hunks[0]!.header.includes("-0,0");
}

/** Compute renderable segments (hunks + expandable gaps) for a parsed file. */
export function computeSegments(parsed: ParsedFile, totalLineCount?: number): DiffSegment[] {
  if (isNewFile(parsed) || parsed.hunks.length === 0) {
    return parsed.hunks.map((hunk, i) => ({ kind: "hunk" as const, hunk, hunkIndex: i }));
  }

  const segments: DiffSegment[] = [];

  for (let i = 0; i < parsed.hunks.length; i++) {
    const hunk = parsed.hunks[i]!;
    const nr = hunkNewRange(hunk);
    const or = hunkOldRange(hunk);

    if (i === 0) {
      // Top gap: lines before first hunk
      if (nr.first > 1) {
        segments.push({
          kind: "gap",
          gap: { startLine: 1, endLine: nr.first - 1, oldStart: 1, totalLines: nr.first - 1 },
          position: "top",
        });
      }
    } else {
      // Middle gap: between previous hunk and this one
      const prevNr = hunkNewRange(parsed.hunks[i - 1]!);
      const prevOr = hunkOldRange(parsed.hunks[i - 1]!);
      const gapNewStart = prevNr.last + 1;
      const gapNewEnd = nr.first - 1;
      if (gapNewStart <= gapNewEnd) {
        segments.push({
          kind: "gap",
          gap: { startLine: gapNewStart, endLine: gapNewEnd, oldStart: prevOr.last + 1, totalLines: gapNewEnd - gapNewStart + 1 },
          position: "middle",
        });
      }
    }

    segments.push({ kind: "hunk", hunk, hunkIndex: i });

    // Bottom gap: after last hunk
    if (i === parsed.hunks.length - 1 && totalLineCount !== undefined && nr.last < totalLineCount) {
      segments.push({
        kind: "gap",
        gap: { startLine: nr.last + 1, endLine: totalLineCount, oldStart: or.last + 1, totalLines: totalLineCount - nr.last },
        position: "bottom",
      });
    }
  }

  return segments;
}

export function parseUnifiedDiff(raw: string, status?: DiffFileStatus): ParsedFile[] {
  const files: ParsedFile[] = [];
  const fileSections = raw.split(/^diff --git /m);

  for (const section of fileSections) {
    if (!section.trim()) continue;

    // Extract file path from "a/path b/path"
    const headerMatch = section.match(/^a\/(.+?) b\/(.+)/m);
    if (!headerMatch?.[2]) continue;
    const path = headerMatch[2];

    const hunks: ParsedHunk[] = [];
    const hunkParts = section.split(/^(@@[^@]+@@.*$)/m);

    for (let i = 1; i < hunkParts.length; i += 2) {
      const header = hunkParts[i]!.trim();
      const body = hunkParts[i + 1] || "";

      // Parse line numbers from @@ -old,count +new,count @@
      const nums = header.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      let oldNum = nums?.[1] ? parseInt(nums[1], 10) : 1;
      let newNum = nums?.[2] ? parseInt(nums[2], 10) : 1;

      const lines: HunkLine[] = [];
      // The body starts with the newline that terminated the @@ header, so
      // the first split element is always an empty artifact — drop it before
      // the loop: the `line === ""` ctx branch below (meant for blank context
      // lines whose trailing space was stripped) would otherwise count it as
      // a phantom first line and shift every old/new number by one.
      const bodyLines = body.split("\n");
      if (bodyLines[0] === "") bodyLines.shift();
      for (const line of bodyLines) {
        if (line.startsWith("+")) {
          lines.push({ type: "add", content: line.slice(1), oldNum: null, newNum });
          newNum++;
        } else if (line.startsWith("-")) {
          lines.push({ type: "del", content: line.slice(1), oldNum, newNum: null });
          oldNum++;
        } else if (line.startsWith(" ") || line === "") {
          // Skip the "\ No newline at end of file" marker
          if (line.startsWith("\\")) continue;
          lines.push({ type: "ctx", content: line.slice(1) || "", oldNum, newNum });
          oldNum++;
          newNum++;
        }
      }

      // Remove trailing empty context lines from parsing artifacts
      while (lines.length > 0) {
        const last = lines[lines.length - 1]!;
        if (last.type === "ctx" && last.content === "") {
          lines.pop();
        } else {
          break;
        }
      }

      if (lines.length > 0) {
        hunks.push({ header, lines });
      }
    }

    files.push({ path, hunks, status });
  }

  return files;
}

function buildLineColors(colors: ColorPalette) {
  return {
    add: { bg: colors.diffAddBg, numBg: colors.diffAddNumBg, text: colors.diffAddText },
    del: { bg: colors.diffDelBg, numBg: colors.diffDelNumBg, text: colors.diffDelText },
  };
}

function statusBadgeColor(status: DiffFileStatus, colors: ColorPalette): string {
  switch (status) {
    case "staged":   return colors.active;
    case "unstaged": return colors.warning;
    case "untracked": return colors.textDim;
    case "conflict": return colors.dangerText;
  }
}

function statusBadgeLabel(status: DiffFileStatus): string {
  if (status === "untracked") return "new";
  return status;
}

// ── Constants ──

const EXPAND_STEP = 20;
const SMALL_GAP_THRESHOLD = 40;

// ── Component ──

interface DiffViewerProps {
  channelId: string | null;
  files: DiffFile[];
  parsedFiles: ParsedFile[];
  expandedFiles: Set<string>;
  loading: boolean;
  hasData: boolean;
  totalFiles: number;
  onToggleFile: (key: string) => void;
  onExpandAll: () => void;
  onCollapseAll: () => void;
  onFileContextMenu: (e: React.MouseEvent, path: string) => void;
}

export function DiffViewer({
  channelId,
  files,
  parsedFiles,
  expandedFiles,
  loading,
  hasData,
  totalFiles,
  onToggleFile,
  onExpandAll,
  onCollapseAll,
  onFileContextMenu,
}: DiffViewerProps) {
  const { colors } = useTheme();
  const fileContentCache = useRef<Map<string, string[]>>(new Map());
  const [expandedGaps, setExpandedGaps] = useState<Map<string, Map<string, { fromTop: number; fromBottom: number }>>>(new Map());
  const [focusedFileIndex, setFocusedFileIndex] = useState(0);
  const fileRefs = useRef<Map<number, HTMLDivElement>>(new Map());
  const scrollRef = useRef<HTMLDivElement>(null);
  const isNavigatingRef = useRef(false);

  // Reset focused index to first file when file list changes
  const fileListSignature = files.map(fileKey).join("\n");
  useEffect(() => {
    setFocusedFileIndex(0);
  }, [fileListSignature]);

  // Update focused file on mouse hover over file sections.
  const handleFileMouseEnter = useCallback((fileIndex: number) => {
    if (isNavigatingRef.current) return;
    setFocusedFileIndex(fileIndex);
  }, []);

  const navigateToFile = useCallback((index: number) => {
    if (index < 0 || index >= files.length) return;
    isNavigatingRef.current = true;
    setFocusedFileIndex(index);
    const file = files[index]!;
    const key = fileKey(file);
    if (!expandedFiles.has(key)) onToggleFile(key);
    requestAnimationFrame(() => {
      fileRefs.current.get(index)?.scrollIntoView({ behavior: "smooth", block: "start" });
      setTimeout(() => { isNavigatingRef.current = false; }, 500);
    });
  }, [files, expandedFiles, onToggleFile]);

  const builtLineColors = buildLineColors(colors);
  const lineColors = {
    add: builtLineColors.add,
    del: builtLineColors.del,
    ctx: { bg: "transparent", numBg: "transparent", text: colors.textMuted },
  };

  // --- Expand context handlers ---
  const ensureFileContent = useCallback(async (filePath: string): Promise<string[] | null> => {
    const cached = fileContentCache.current.get(filePath);
    if (cached) return cached;
    if (!channelId) return null;
    try {
      const { content, binary } = await fetchFileContent(channelId, filePath);
      if (binary) return null;
      const lines = content.split("\n");
      fileContentCache.current.set(filePath, lines);
      return lines;
    } catch {
      return null;
    }
  }, [channelId]);

  const gapKey = (gap: ExpandableGap) => `${gap.startLine}-${gap.endLine}`;

  const handleExpand = useCallback(async (filePath: string, gap: ExpandableGap, direction: "up" | "down" | "all") => {
    await ensureFileContent(filePath);
    setExpandedGaps((prev) => {
      const fileGaps = new Map(prev.get(filePath) ?? []);
      const current = fileGaps.get(gapKey(gap)) ?? { fromTop: 0, fromBottom: 0 };
      if (direction === "down") {
        fileGaps.set(gapKey(gap), { ...current, fromTop: current.fromTop + EXPAND_STEP });
      } else if (direction === "up") {
        fileGaps.set(gapKey(gap), { ...current, fromBottom: current.fromBottom + EXPAND_STEP });
      } else {
        fileGaps.set(gapKey(gap), { fromTop: gap.totalLines, fromBottom: 0 });
      }
      const next = new Map(prev);
      next.set(filePath, fileGaps);
      return next;
    });
  }, [ensureFileContent]);

  /** Render a block of HunkLine[] with the standard gutter + content layout. */
  const renderLines = (lines: HunkLine[]) => (
    <div style={{ display: "flex" }}>
      <div style={{ flexShrink: 0 }}>
        {lines.map((line, li) => {
          const lc = lineColors[line.type];
          return (
            <div key={li} style={{ display: "flex", lineHeight: "20px", fontFamily: fonts.mono, backgroundColor: lc.bg }}>
              <span style={{ width: 40, textAlign: "right", paddingRight: 4, color: colors.textDim, backgroundColor: lc.numBg, userSelect: "none", fontSize: 11 }}>{line.oldNum ?? ""}</span>
              <span style={{ width: 40, textAlign: "right", paddingRight: 8, color: colors.textDim, backgroundColor: lc.numBg, userSelect: "none", fontSize: 11 }}>{line.newNum ?? ""}</span>
              <span style={{ width: 14, textAlign: "center", color: line.type === "add" ? colors.diffAddText : line.type === "del" ? colors.diffDelText : "transparent", userSelect: "none" }}>
                {line.type === "add" ? "+" : line.type === "del" ? "\u2212" : " "}
              </span>
            </div>
          );
        })}
      </div>
      <div style={{ flex: 1, overflowX: "auto", minWidth: 0 }}>
        <div style={{ display: "inline-block", minWidth: "100%" }}>
          {lines.map((line, li) => {
            const lc = lineColors[line.type];
            return (
              <div key={li} style={{ lineHeight: "20px", fontFamily: fonts.mono, fontSize: 12, whiteSpace: "pre", color: lc.text, backgroundColor: lc.bg, paddingRight: 8 }}>
                {line.content || " "}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );

  /** Build context HunkLine[] from cached file content for a gap region. */
  const buildContextLines = (filePath: string, startLine: number, endLine: number, oldStart: number): HunkLine[] => {
    const cached = fileContentCache.current.get(filePath);
    if (!cached) return [];
    const lines: HunkLine[] = [];
    for (let i = startLine; i <= endLine && i <= cached.length; i++) {
      lines.push({ type: "ctx", content: cached[i - 1] ?? "", oldNum: oldStart + (i - startLine), newNum: i });
    }
    return lines;
  };

  /** Render an expand row for a gap. */
  const renderExpandRow = (filePath: string, gap: ExpandableGap, position: "top" | "middle" | "bottom", remaining: number) => {
    const expandBtnStyle: React.CSSProperties = {
      background: "none", border: "none", color: colors.active, cursor: "pointer",
      fontSize: 11, fontFamily: fonts.mono, padding: "0 8px", lineHeight: "20px",
    };
    const showAll = remaining <= SMALL_GAP_THRESHOLD;
    return (
      <div style={{ display: "flex", lineHeight: "20px", fontFamily: fonts.mono, backgroundColor: colors.diffHunkBg, userSelect: "none" }}>
        <span style={{ width: 40, textAlign: "right", paddingRight: 4, color: colors.textDim, fontSize: 11 }}>···</span>
        <span style={{ width: 40, textAlign: "right", paddingRight: 8, color: colors.textDim, fontSize: 11 }}>···</span>
        <span style={{ width: 14 }} />
        <span style={{ flex: 1, display: "flex", alignItems: "center", gap: 4 }}>
          {(position === "top" || position === "middle") && !showAll && (
            <button style={expandBtnStyle} onClick={() => handleExpand(filePath, gap, "down")}
              onMouseEnter={(e) => { e.currentTarget.style.textDecoration = "underline"; }}
              onMouseLeave={(e) => { e.currentTarget.style.textDecoration = "none"; }}>
              ↓ {Math.min(EXPAND_STEP, remaining)} lines
            </button>
          )}
          {showAll ? (
            <button style={expandBtnStyle} onClick={() => handleExpand(filePath, gap, "all")}
              onMouseEnter={(e) => { e.currentTarget.style.textDecoration = "underline"; }}
              onMouseLeave={(e) => { e.currentTarget.style.textDecoration = "none"; }}>
              Load all {remaining} lines
            </button>
          ) : (
            <button style={expandBtnStyle} onClick={() => handleExpand(filePath, gap, "all")}
              onMouseEnter={(e) => { e.currentTarget.style.textDecoration = "underline"; }}
              onMouseLeave={(e) => { e.currentTarget.style.textDecoration = "none"; }}>
              ↕ all {remaining}
            </button>
          )}
          {(position === "bottom" || position === "middle") && !showAll && (
            <button style={expandBtnStyle} onClick={() => handleExpand(filePath, gap, "up")}
              onMouseEnter={(e) => { e.currentTarget.style.textDecoration = "underline"; }}
              onMouseLeave={(e) => { e.currentTarget.style.textDecoration = "none"; }}>
              ↑ {Math.min(EXPAND_STEP, remaining)} lines
            </button>
          )}
        </span>
      </div>
    );
  };

  /** Render a gap segment: revealed context lines + remaining expand row. */
  const renderGapSegment = (filePath: string, gap: ExpandableGap, position: "top" | "middle" | "bottom") => {
    const fileGaps = expandedGaps.get(filePath);
    const state = fileGaps?.get(gapKey(gap)) ?? { fromTop: 0, fromBottom: 0 };
    const revealedTop = Math.min(state.fromTop, gap.totalLines);
    const revealedBottom = Math.min(state.fromBottom, gap.totalLines - revealedTop);
    const remaining = gap.totalLines - revealedTop - revealedBottom;

    return (
      <>
        {revealedTop > 0 && renderLines(buildContextLines(filePath, gap.startLine, gap.startLine + revealedTop - 1, gap.oldStart))}
        {remaining > 0 && renderExpandRow(filePath, gap, position, remaining)}
        {revealedBottom > 0 && renderLines(buildContextLines(filePath, gap.endLine - revealedBottom + 1, gap.endLine, gap.oldStart + (gap.totalLines - revealedBottom)))}
      </>
    );
  };

  const navBtnStyle: React.CSSProperties = {
    display: "flex", alignItems: "center", justifyContent: "center",
    width: 24, height: 24, border: `1px solid ${colors.border}`, borderRadius: 4,
    background: "transparent", color: colors.textMuted, cursor: "pointer", fontSize: 14, lineHeight: 1,
  };

  const anyExpanded = expandedFiles.size > 0;
  const clampedIndex = anyExpanded ? Math.min(focusedFileIndex, Math.max(files.length - 1, 0)) : 0;
  const focusedPath = anyExpanded ? (files[clampedIndex]?.path ?? "") : "";

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      {/* File navigation bar */}
      {files.length > 0 && (
        <div style={{
          display: "flex", alignItems: "center", gap: 8, padding: "4px 12px",
          background: colors.surface, borderBottom: `1px solid ${colors.border}`,
          minHeight: 32, flexShrink: 0,
        }}>
          <button onClick={onExpandAll} title="Expand all" style={navBtnStyle}
            onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; e.currentTarget.style.color = colors.textLight; }}
            onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.color = colors.textMuted; }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="7,8 12,13 17,8" /><polyline points="7,14 12,19 17,14" /></svg>
          </button>
          <button onClick={onCollapseAll} title="Collapse all" style={navBtnStyle}
            onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; e.currentTarget.style.color = colors.textLight; }}
            onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.color = colors.textMuted; }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="7,14 12,9 17,14" /><polyline points="7,20 12,15 17,20" /></svg>
          </button>
          <button
            style={{ ...navBtnStyle, opacity: clampedIndex <= 0 ? 0.3 : 1, cursor: clampedIndex <= 0 ? "default" : "pointer" }}
            disabled={clampedIndex <= 0}
            onClick={() => navigateToFile(clampedIndex - 1)}
            title="Previous file"
          >
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M2.5 6.5L5 3.5L7.5 6.5" /></svg>
          </button>
          <button
            style={{ ...navBtnStyle, opacity: clampedIndex >= files.length - 1 ? 0.3 : 1, cursor: clampedIndex >= files.length - 1 ? "default" : "pointer" }}
            disabled={clampedIndex >= files.length - 1}
            onClick={() => navigateToFile(clampedIndex + 1)}
            title="Next file"
          >
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M2.5 3.5L5 6.5L7.5 3.5" /></svg>
          </button>
          <span style={{ flex: 1, fontFamily: fonts.mono, fontSize: 12, color: colors.textLight, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {focusedPath}
          </span>
          <span style={{ fontFamily: fonts.mono, fontSize: 11, color: colors.textDim, flexShrink: 0 }}>
            {clampedIndex + 1} / {files.length}
          </span>
        </div>
      )}
      <div ref={scrollRef} style={{ flex: 1, overflow: "auto", minHeight: 0 }}>
        {loading && !hasData && (
          <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>Loading...</div>
        )}
        {hasData && totalFiles === 0 && (
          <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>No changes</div>
        )}
        {files.map((file, fileIndex) => {
          const key = fileKey(file);
          const expanded = expandedFiles.has(key);
          const focused = expandedFiles.size > 0 && fileIndex === clampedIndex;
          const parsed = parsedFiles.find((pf) => pf.path === file.path && pf.status === file.status);
          const cachedLines = fileContentCache.current.get(file.path);
          const segments = parsed ? computeSegments(parsed, cachedLines?.length) : [];
          return (
            <div
              key={`${key}:${fileIndex}`}
              data-file-idx={fileIndex}
              ref={(el) => { if (el) fileRefs.current.set(fileIndex, el); else fileRefs.current.delete(fileIndex); }}
              onMouseEnter={() => handleFileMouseEnter(fileIndex)}
              style={{ borderLeft: `3px solid ${focused ? colors.active : "transparent"}`, transition: "border-color 0.15s ease" }}
            >
              <button
                onClick={() => { setFocusedFileIndex(fileIndex); onToggleFile(key); }}
                onContextMenu={(e) => { e.preventDefault(); onFileContextMenu(e, file.path); }}
                style={{
                  display: "flex", alignItems: "center", gap: 6, width: "100%",
                  padding: "4px 12px", border: "none",
                  background: expanded ? colors.hoverBg : "transparent",
                  color: colors.textLight, fontSize: 12, fontFamily: fonts.mono,
                  textAlign: "left", cursor: "pointer",
                }}
                onMouseEnter={(e) => { if (!expanded) e.currentTarget.style.background = colors.hoverBg; }}
                onMouseLeave={(e) => { if (!expanded) e.currentTarget.style.background = "transparent"; }}
              >
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"
                  style={{ transition: "transform 0.15s ease", transform: expanded ? "rotate(0deg)" : "rotate(-90deg)", flexShrink: 0, color: colors.textDim }}>
                  <path d="M2.5 3.5L5 6.5L7.5 3.5" />
                </svg>
                <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", direction: "rtl", textAlign: "left" }}>
                  <bdi>{file.old_path ? formatRenamePath(file.old_path, file.path) : file.path}</bdi>
                </span>
                <span style={{ flexShrink: 0, width: 70, textAlign: "left" }}>
                  {file.status && (
                    <span style={{
                      display: "inline-block", fontSize: 10, fontFamily: fonts.mono,
                      padding: "1px 5px", borderRadius: 3,
                      color: statusBadgeColor(file.status, colors),
                      border: `1px solid ${statusBadgeColor(file.status, colors)}`,
                      textTransform: "lowercase",
                    }}>
                      {statusBadgeLabel(file.status)}
                    </span>
                  )}
                </span>
                <span style={{ flexShrink: 0, width: 80, fontSize: 11, textAlign: "right" }}>
                  {file.binary ? (
                    <span style={{ color: colors.textDim }}>binary</span>
                  ) : (
                    <><span style={{ color: colors.diffAddText }}>+{file.additions}</span>{" "}<span style={{ color: colors.diffDelText }}>-{file.deletions}</span></>
                  )}
                </span>
              </button>
              {expanded && parsed && (
                <div style={{ borderBottom: `1px solid ${colors.border}`, overflow: "hidden" }}>
                  {segments.map((seg, si) => (
                    <div key={si}>
                      {seg.kind === "hunk" ? (
                        <>
                          <div style={{ padding: "2px 12px", fontSize: 11, fontFamily: fonts.mono, color: colors.textDim, backgroundColor: colors.diffHunkBg, whiteSpace: "pre", overflow: "hidden", textOverflow: "ellipsis" }}>
                            {seg.hunk.header}
                          </div>
                          {renderLines(seg.hunk.lines)}
                        </>
                      ) : (
                        renderGapSegment(file.path, seg.gap, seg.position)
                      )}
                    </div>
                  ))}
                  {file.binary && (
                    <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>Binary file — no content preview</div>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
