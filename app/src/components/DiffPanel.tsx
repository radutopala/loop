import { useCallback, useEffect, useRef, useState } from "react";
import type { DiffResponse } from "../api/loopApi";
import { fetchDiff, fetchFileContent, fetchBranches } from "../api/loopApi";
import { useEventStream } from "../hooks/useEventStream";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";
import { ContextMenu } from "./ContextMenu";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.6;
const POLL_INTERVAL = 5_000;
const WIDTH_STORAGE_KEY = "loop-diff-panel-width";
const EXPAND_STEP = 20;
const SMALL_GAP_THRESHOLD = 40;

function loadWidth(): number {
  try {
    const stored = localStorage.getItem(WIDTH_STORAGE_KEY);
    if (stored) {
      const w = parseInt(stored, 10);
      if (w >= MIN_WIDTH) return w;
    }
  } catch { /* ignore */ }
  // Default to max width on first open
  return Math.floor(window.innerWidth * MAX_WIDTH_PERCENT);
}

function saveWidth(w: number) {
  try {
    localStorage.setItem(WIDTH_STORAGE_KEY, String(w));
  } catch { /* ignore */ }
}

interface DiffPanelProps {
  channelId: string | null;
  dirPath?: string;
  branch?: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  tabBar?: React.ReactNode;
  embedded?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

interface ParsedHunk {
  header: string;
  lines: HunkLine[];
}

interface HunkLine {
  type: "add" | "del" | "ctx";
  content: string;
  oldNum: number | null;
  newNum: number | null;
}

interface ParsedFile {
  path: string;
  hunks: ParsedHunk[];
}

interface ExpandableGap {
  startLine: number;   // 1-based new-file line where gap starts
  endLine: number;     // 1-based new-file line where gap ends (inclusive)
  oldStart: number;    // corresponding old-file line number
  totalLines: number;
}

type DiffSegment =
  | { kind: "hunk"; hunk: ParsedHunk; hunkIndex: number }
  | { kind: "gap"; gap: ExpandableGap; position: "top" | "middle" | "bottom" };

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
function computeSegments(parsed: ParsedFile, totalLineCount?: number): DiffSegment[] {
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

function parseUnifiedDiff(raw: string): ParsedFile[] {
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
      for (const line of body.split("\n")) {
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

    files.push({ path, hunks });
  }

  return files;
}

function buildLineColors(colors: ColorPalette) {
  return {
    add: { bg: colors.diffAddBg, numBg: colors.diffAddNumBg, text: colors.diffAddText },
    del: { bg: colors.diffDelBg, numBg: colors.diffDelNumBg, text: colors.diffDelText },
  };
}

function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: 4,
    lineHeight: 1,
    borderRadius: 4,
    display: "flex",
    alignItems: "center",
  };
}

export function DiffPanel({ channelId, dirPath, branch, maximized, sidebarOpen, tabBar, embedded, onToggleSidebar, onOpenPalette, onToggleMaximize, onClose }: DiffPanelProps) {
  const { colors } = useTheme();
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const [data, setData] = useState<DiffResponse | null>(null);
  const [parsedFiles, setParsedFiles] = useState<ParsedFile[]>([]);
  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [fileContextMenu, setFileContextMenu] = useState<{ x: number; y: number; path: string } | null>(null);
  // Branch diff mode state
  type DiffMode = "changes" | "branches";
  const [diffMode, setDiffMode] = useState<DiffMode>("changes");
  const [branches, setBranches] = useState<string[]>([]);
  const [sourceBranch, setSourceBranch] = useState<string>("");
  const [targetBranch, setTargetBranch] = useState<string>("");
  // Expand context state: file content cache and per-gap expansion tracking
  const fileContentCache = useRef<Map<string, string[]>>(new Map());
  const [expandedGaps, setExpandedGaps] = useState<Map<string, Map<string, { fromTop: number; fromBottom: number }>>>(new Map());
  const prevDiffRef = useRef<string>("");
  const panelRef = useRef<HTMLDivElement>(null);

  const headerBtnStyle = buildHeaderBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  const builtLineColors = buildLineColors(colors);
  const lineColors = {
    add: builtLineColors.add,
    del: builtLineColors.del,
    ctx: { bg: "transparent", numBg: "transparent", text: colors.textMuted },
  };

  // Fetch branch list when switching to branch mode
  useEffect(() => {
    if (diffMode === "branches" && channelId) {
      fetchBranches(channelId).then((info) => {
        // Combine regular branches + current branch (which may be filtered out of the list).
        const all = new Set(info.branches);
        if (info.current) all.add(info.current);
        // Also include worktree branches.
        for (const wt of info.worktrees) {
          if (wt.branch) all.add(wt.branch);
        }
        const sorted = [...all].sort();
        setBranches(sorted);
        // Default source to main branch, target to current branch
        if (!sourceBranch) {
          const main = sorted.find((b) => b === "main" || b === "master");
          setSourceBranch(main ?? info.current ?? sorted[0] ?? "");
        }
        if (!targetBranch && info.current) {
          setTargetBranch(info.current);
        }
      }).catch(() => {});
    }
  }, [diffMode, channelId]); // eslint-disable-line react-hooks/exhaustive-deps

  const load = useCallback(async () => {
    if (!channelId) return;
    // In branch mode, both branches must be selected
    if (diffMode === "branches" && (!sourceBranch || !targetBranch)) return;
    try {
      const d = diffMode === "branches"
        ? await fetchDiff(channelId, sourceBranch, targetBranch)
        : await fetchDiff(channelId);
      // If diff changed, invalidate file content cache and expanded gaps
      if (d.diff !== prevDiffRef.current) {
        fileContentCache.current.clear();
        setExpandedGaps(new Map());
        prevDiffRef.current = d.diff;
      }
      setData(d);
      setParsedFiles(parseUnifiedDiff(d.diff));
    } catch {
      /* ignore fetch errors — will retry on next poll */
    } finally {
      setLoading(false);
    }
  }, [channelId, diffMode, sourceBranch, targetBranch]);

  // Initial load + background polling fallback
  useEffect(() => {
    setLoading(true);
    setData(null);
    setParsedFiles([]);
    setExpandedFiles(new Set());
    load();
    const id = setInterval(load, POLL_INTERVAL);
    return () => clearInterval(id);
  }, [load]);

  // Real-time refresh: debounce-reload on any agent event for this channel.
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onEvent = useCallback(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(load, 1_000);
  }, [load]);
  useEffect(() => () => { if (debounceRef.current) clearTimeout(debounceRef.current); }, []);
  useEventStream({ channelId, onEvent });

  const toggleFile = useCallback((path: string) => {
    setExpandedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  }, []);

  const expandAll = useCallback(() => {
    if (data) {
      setExpandedFiles(new Set(data.files.map((f) => f.path)));
    }
  }, [data]);

  const collapseAll = useCallback(() => {
    setExpandedFiles(new Set());
  }, []);

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

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setResizing(true);
      const startX = e.clientX;
      const startWidth = width;

      let lastWidth = startWidth;
      const onMouseMove = (ev: MouseEvent) => {
        const maxWidth = window.innerWidth * MAX_WIDTH_PERCENT;
        const newWidth = Math.min(maxWidth, Math.max(MIN_WIDTH, startWidth - (ev.clientX - startX)));
        lastWidth = newWidth;
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        setResizing(false);
        saveWidth(lastWidth);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width],
  );

  const totalFiles = data?.files.length ?? 0;
  const totalAdd = data?.total_additions ?? 0;
  const totalDel = data?.total_deletions ?? 0;

  const selectStyle: React.CSSProperties = {
    background: colors.surface,
    color: colors.textLight,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    fontSize: 11,
    fontFamily: fonts.mono,
    padding: "1px 4px",
    outline: "none",
    maxWidth: 120,
    cursor: "pointer",
  };

  const modeTabStyle = (active: boolean): React.CSSProperties => ({
    background: "none",
    border: "none",
    borderBottom: active ? `2px solid ${colors.active}` : "2px solid transparent",
    color: active ? colors.textLight : colors.textDim,
    cursor: "pointer",
    fontSize: 10,
    fontWeight: 700,
    textTransform: "uppercase",
    letterSpacing: 1,
    padding: "0 4px 2px",
    lineHeight: "20px",
  });

  const diffToolbar = (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        borderBottom: `1px solid ${colors.border}`,
        flexShrink: 0,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 4,
          padding: "3px 8px",
          height: 28,
          boxSizing: "border-box",
        }}
      >
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <button style={modeTabStyle(diffMode === "changes")} onClick={() => setDiffMode("changes")}>Changes</button>
          <button style={modeTabStyle(diffMode === "branches")} onClick={() => setDiffMode("branches")}>Branches</button>
          {totalFiles > 0 && <span style={{ fontSize: 10, color: colors.textDim }}>{totalFiles}</span>}
          {(totalAdd > 0 || totalDel > 0) && (
            <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
              <span style={{ color: colors.diffAddText }}>+{totalAdd}</span>{" "}
              <span style={{ color: colors.diffDelText }}>-{totalDel}</span>
            </span>
          )}
        </span>
        <div style={{ flex: 1 }} />
        {totalFiles > 0 && (
          <>
            <button onClick={expandAll} title="Expand all" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="7,8 12,13 17,8" /><polyline points="7,14 12,19 17,14" /></svg>
            </button>
            <button onClick={collapseAll} title="Collapse all" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="7,14 12,9 17,14" /><polyline points="7,20 12,15 17,20" /></svg>
            </button>
          </>
        )}
        <button onClick={load} title="Refresh" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12a9 9 0 1 1-3-6.7" /><polyline points="21,3 21,9 15,9" /></svg>
        </button>
      </div>
      {diffMode === "branches" && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 8px", fontSize: 11, color: colors.textDim }}>
          <select
            value={sourceBranch}
            onChange={(e) => setSourceBranch(e.target.value)}
            style={selectStyle}
            title="Source branch (base)"
          >
            {!sourceBranch && <option value="">source…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
          <span style={{ color: colors.textDim, fontSize: 11, flexShrink: 0 }}>→</span>
          <select
            value={targetBranch}
            onChange={(e) => setTargetBranch(e.target.value)}
            style={selectStyle}
            title="Target branch (compare)"
          >
            {!targetBranch && <option value="">target…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </div>
      )}
    </div>
  );

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

  const diffContent = (
    <div style={{ flex: 1, overflow: "auto" }}>
      {loading && !data && (
        <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>Loading...</div>
      )}
      {data && totalFiles === 0 && (
        <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>No changes</div>
      )}
      {data?.files.map((file) => {
        const expanded = expandedFiles.has(file.path);
        const parsed = parsedFiles.find((pf) => pf.path === file.path);
        const cachedLines = fileContentCache.current.get(file.path);
        const segments = parsed ? computeSegments(parsed, cachedLines?.length) : [];
        return (
          <div key={file.path}>
            <button
              onClick={() => toggleFile(file.path)}
              onContextMenu={(e) => { e.preventDefault(); setFileContextMenu({ x: e.clientX, y: e.clientY, path: file.path }); }}
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
                <bdi>{file.path}</bdi>
              </span>
              <span style={{ flexShrink: 0, fontSize: 11 }}>
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
  );

  if (embedded) {
    return (
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar }}>
        {diffToolbar}
        {diffContent}
        {fileContextMenu && (
          <ContextMenu
            x={fileContextMenu.x}
            y={fileContextMenu.y}
            items={[
              { label: "Copy relative path", onClick: () => navigator.clipboard.writeText(fileContextMenu.path) },
              { label: "Copy absolute path", onClick: () => navigator.clipboard.writeText((dirPath || "") + "/" + fileContextMenu.path) },
            ]}
            onClose={() => setFileContextMenu(null)}
          />
        )}
      </div>
    );
  }

  return (
    <div
      ref={panelRef}
      style={{
        width: maximized ? "100%" : width,
        minWidth: maximized ? 0 : MIN_WIDTH,
        maxWidth: maximized ? "none" : `${MAX_WIDTH_PERCENT * 100}vw`,
        flex: maximized ? 1 : undefined,
        flexShrink: maximized ? undefined : 1,
        backgroundColor: colors.sidebar,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        userSelect: resizing ? "none" : undefined,
        borderLeft: maximized ? "none" : `1px solid ${colors.border}`,
      }}
    >
      {/* Resize handle (left edge) — hidden when maximized */}
      {!maximized && (
        <div
          onMouseDown={handleMouseDown}
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            width: 4,
            height: "100%",
            cursor: "col-resize",
            backgroundColor: resizing ? colors.textDim : "transparent",
            zIndex: 1,
          }}
          onMouseEnter={(e) => { (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim; }}
          onMouseLeave={(e) => { if (!resizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent"; }}
        />
      )}

      {/* Drag region for macOS title bar alignment */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: maximized && !sidebarOpen ? 76 : maximized ? 4 : 0,
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      >
        {maximized && onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
        {maximized && onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              marginLeft: 6,
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
        )}
        {maximized && dirPath && (
          <span
            style={{
              fontSize: 12,
              color: colors.textDim,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              minWidth: 0,
              display: "flex",
              alignItems: "center",
              gap: 6,
              marginLeft: 12,
            }}
          >
            {dirPath}
            {branch && (
              <>
                <span style={{ color: colors.border, flexShrink: 0 }}>|</span>
                <span style={{ fontSize: 11, color: colors.active, fontFamily: fonts.mono, flexShrink: 0 }}>
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2, verticalAlign: -1 }}>
                    <line x1="6" y1="3" x2="6" y2="15" />
                    <circle cx="18" cy="6" r="3" />
                    <circle cx="6" cy="18" r="3" />
                    <path d="M18 9a9 9 0 0 1-9 9" />
                  </svg>
                  {branch}
                </span>
              </>
            )}
          </span>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Header — sized to match the main toolbar height so bottom borders align */}
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
        }}
      >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          flexShrink: 0,
          boxSizing: "border-box",
          height: 35,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, overflow: "hidden" }}>
          {maximized && tabBar}
          {!maximized && (
            <>
              <button style={modeTabStyle(diffMode === "changes")} onClick={() => setDiffMode("changes")}>Changes</button>
              <button style={modeTabStyle(diffMode === "branches")} onClick={() => setDiffMode("branches")}>Branches</button>
              {totalFiles > 0 && (
                <span style={{ fontSize: 10, color: colors.textDim }}>
                  {totalFiles}
                </span>
              )}
              {(totalAdd > 0 || totalDel > 0) && (
                <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
                  <span style={{ color: colors.diffAddText }}>+{totalAdd}</span>
                  {" "}
                  <span style={{ color: colors.diffDelText }}>-{totalDel}</span>
                </span>
              )}
            </>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
          {maximized && (
            <span style={{ display: "flex", alignItems: "center", gap: 8, marginRight: 8 }}>
              <button style={modeTabStyle(diffMode === "changes")} onClick={() => setDiffMode("changes")}>Changes</button>
              <button style={modeTabStyle(diffMode === "branches")} onClick={() => setDiffMode("branches")}>Branches</button>
              {totalFiles > 0 && (
                <span style={{ fontSize: 10, color: colors.textDim }}>
                  {totalFiles}
                </span>
              )}
              {(totalAdd > 0 || totalDel > 0) && (
                <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
                  <span style={{ color: colors.diffAddText }}>+{totalAdd}</span>
                  {" "}
                  <span style={{ color: colors.diffDelText }}>-{totalDel}</span>
                </span>
              )}
            </span>
          )}
          {totalFiles > 0 && (
            <>
              <button onClick={expandAll} title="Expand all" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="7,8 12,13 17,8" />
                  <polyline points="7,14 12,19 17,14" />
                </svg>
              </button>
              <button onClick={collapseAll} title="Collapse all" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="7,14 12,9 17,14" />
                  <polyline points="7,20 12,15 17,20" />
                </svg>
              </button>
            </>
          )}
          <button onClick={load} title="Refresh" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 12a9 9 0 1 1-3-6.7" />
              <polyline points="21,3 21,9 15,9" />
            </svg>
          </button>
          {onToggleMaximize && (
            <button onClick={onToggleMaximize} title={maximized ? "Restore panel" : "Maximize panel"} style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
              {maximized ? (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4,14 10,14 10,20" />
                  <polyline points="20,10 14,10 14,4" />
                  <line x1="14" y1="10" x2="21" y2="3" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="15,3 21,3 21,9" />
                  <polyline points="9,21 3,21 3,15" />
                  <line x1="21" y1="3" x2="14" y2="10" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              )}
            </button>
          )}
          <button onClick={onClose} title="Close panel" style={headerBtnStyle} onMouseEnter={hoverIn} onMouseLeave={hoverOut}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>
      {diffMode === "branches" && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "3px 12px", fontSize: 11, color: colors.textDim }}>
          <select
            value={sourceBranch}
            onChange={(e) => setSourceBranch(e.target.value)}
            style={selectStyle}
            title="Source branch (base)"
          >
            {!sourceBranch && <option value="">source…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
          <span style={{ color: colors.textDim, fontSize: 11, flexShrink: 0 }}>→</span>
          <select
            value={targetBranch}
            onChange={(e) => setTargetBranch(e.target.value)}
            style={selectStyle}
            title="Target branch (compare)"
          >
            {!targetBranch && <option value="">target…</option>}
            {branches.map((b) => <option key={b} value={b}>{b}</option>)}
          </select>
        </div>
      )}
      </div>

      {/* File list + diffs */}
      {diffContent}
      {fileContextMenu && (
        <ContextMenu
          x={fileContextMenu.x}
          y={fileContextMenu.y}
          items={[
              { label: "Copy relative path", onClick: () => navigator.clipboard.writeText(fileContextMenu.path) },
              { label: "Copy absolute path", onClick: () => navigator.clipboard.writeText((dirPath || "") + "/" + fileContextMenu.path) },
            ]}
          onClose={() => setFileContextMenu(null)}
        />
      )}
    </div>
  );
}
