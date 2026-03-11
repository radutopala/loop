import { useCallback, useEffect, useRef, useState } from "react";
import type { DiffResponse } from "../api/loopApi";
import { fetchDiff } from "../api/loopApi";
import { useEventStream } from "../hooks/useEventStream";
import { colors, fonts } from "../theme";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.45;
const POLL_INTERVAL = 5_000;
const WIDTH_STORAGE_KEY = "loop-diff-panel-width";

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

const lineColors = {
  add: { bg: "rgba(34, 197, 94, 0.12)", numBg: "rgba(34, 197, 94, 0.2)", text: "#86efac" },
  del: { bg: "rgba(239, 68, 68, 0.12)", numBg: "rgba(239, 68, 68, 0.2)", text: "#fca5a5" },
  ctx: { bg: "transparent", numBg: "transparent", text: colors.textMuted },
};

export function DiffPanel({ channelId, dirPath, branch, maximized, sidebarOpen, onToggleSidebar, onOpenPalette, onToggleMaximize, onClose }: DiffPanelProps) {
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);
  const [data, setData] = useState<DiffResponse | null>(null);
  const [parsedFiles, setParsedFiles] = useState<ParsedFile[]>([]);
  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);

  const load = useCallback(async () => {
    if (!channelId) return;
    try {
      const d = await fetchDiff(channelId);
      setData(d);
      setParsedFiles(parseUnifiedDiff(d.diff));
    } catch {
      /* ignore fetch errors — will retry on next poll */
    } finally {
      setLoading(false);
    }
  }, [channelId]);

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
      </div>

      {/* Header — sized to match the main toolbar height so bottom borders align */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          boxSizing: "border-box",
          height: 39,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, overflow: "hidden" }}>
          <span
            style={{
              fontSize: 10,
              fontWeight: 700,
              color: colors.textDim,
              textTransform: "uppercase",
              letterSpacing: 1,
              flexShrink: 0,
            }}
          >
            Changes
          </span>
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
          {totalFiles > 0 && (
            <span style={{ fontSize: 10, color: colors.textDim }}>
              {totalFiles}
            </span>
          )}
          {(totalAdd > 0 || totalDel > 0) && (
            <span style={{ fontSize: 10, fontFamily: fonts.mono }}>
              <span style={{ color: "#86efac" }}>+{totalAdd}</span>
              {" "}
              <span style={{ color: "#fca5a5" }}>-{totalDel}</span>
            </span>
          )}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
          {totalFiles > 0 && (
            <>
              <button
                onClick={expandAll}
                title="Expand all"
                style={headerBtnStyle}
                onMouseEnter={hoverIn}
                onMouseLeave={hoverOut}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="7,8 12,13 17,8" />
                  <polyline points="7,14 12,19 17,14" />
                </svg>
              </button>
              <button
                onClick={collapseAll}
                title="Collapse all"
                style={headerBtnStyle}
                onMouseEnter={hoverIn}
                onMouseLeave={hoverOut}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="7,14 12,9 17,14" />
                  <polyline points="7,20 12,15 17,20" />
                </svg>
              </button>
            </>
          )}
          <button
            onClick={load}
            title="Refresh"
            style={headerBtnStyle}
            onMouseEnter={hoverIn}
            onMouseLeave={hoverOut}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 12a9 9 0 1 1-3-6.7" />
              <polyline points="21,3 21,9 15,9" />
            </svg>
          </button>
          {onToggleMaximize && (
            <button
              onClick={onToggleMaximize}
              title={maximized ? "Restore panel" : "Maximize panel"}
              style={headerBtnStyle}
              onMouseEnter={hoverIn}
              onMouseLeave={hoverOut}
            >
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
          <button
            onClick={onClose}
            title="Close panel"
            style={headerBtnStyle}
            onMouseEnter={hoverIn}
            onMouseLeave={hoverOut}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>

      {/* File list + diffs */}
      <div style={{ flex: 1, overflow: "auto" }}>
        {loading && !data && (
          <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>
            Loading...
          </div>
        )}

        {data && totalFiles === 0 && (
          <div style={{ padding: "20px 12px", color: colors.textDim, fontSize: 13 }}>
            No changes
          </div>
        )}

        {data?.files.map((file) => {
          const expanded = expandedFiles.has(file.path);
          const parsed = parsedFiles.find((pf) => pf.path === file.path);
          return (
            <div key={file.path}>
              <button
                onClick={() => toggleFile(file.path)}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  width: "100%",
                  padding: "4px 12px",
                  border: "none",
                  background: expanded ? colors.hoverBg : "transparent",
                  color: colors.textLight,
                  fontSize: 12,
                  fontFamily: fonts.mono,
                  textAlign: "left",
                  cursor: "pointer",
                }}
                onMouseEnter={(e) => { if (!expanded) e.currentTarget.style.background = colors.hoverBg; }}
                onMouseLeave={(e) => { if (!expanded) e.currentTarget.style.background = "transparent"; }}
              >
                <svg
                  width="10"
                  height="10"
                  viewBox="0 0 10 10"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  style={{
                    transition: "transform 0.15s ease",
                    transform: expanded ? "rotate(0deg)" : "rotate(-90deg)",
                    flexShrink: 0,
                    color: colors.textDim,
                  }}
                >
                  <path d="M2.5 3.5L5 6.5L7.5 3.5" />
                </svg>
                <span
                  style={{
                    flex: 1,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    direction: "rtl",
                    textAlign: "left",
                  }}
                >
                  <bdi>{file.path}</bdi>
                </span>
                <span style={{ flexShrink: 0, fontSize: 11 }}>
                  {file.binary ? (
                    <span style={{ color: colors.textDim }}>binary</span>
                  ) : (
                    <>
                      <span style={{ color: "#86efac" }}>+{file.additions}</span>
                      {" "}
                      <span style={{ color: "#fca5a5" }}>-{file.deletions}</span>
                    </>
                  )}
                </span>
              </button>

              {expanded && parsed && (
                <div style={{ borderBottom: `1px solid ${colors.border}`, overflow: "hidden" }}>
                  {parsed.hunks.map((hunk, hi) => (
                    <div key={hi}>
                      <div
                        style={{
                          padding: "2px 12px",
                          fontSize: 11,
                          fontFamily: fonts.mono,
                          color: colors.textDim,
                          backgroundColor: "rgba(100, 100, 100, 0.1)",
                          whiteSpace: "pre",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                        }}
                      >
                        {hunk.header}
                      </div>
                      <div style={{ display: "flex" }}>
                        {/* Fixed gutter */}
                        <div style={{ flexShrink: 0 }}>
                          {hunk.lines.map((line, li) => {
                            const lc = lineColors[line.type];
                            return (
                              <div
                                key={li}
                                style={{
                                  display: "flex",
                                  lineHeight: "20px",
                                  fontFamily: fonts.mono,
                                  backgroundColor: lc.bg,
                                }}
                              >
                                <span
                                  style={{
                                    width: 40,
                                    textAlign: "right",
                                    paddingRight: 4,
                                    color: colors.textDim,
                                    backgroundColor: lc.numBg,
                                    userSelect: "none",
                                    fontSize: 11,
                                  }}
                                >
                                  {line.oldNum ?? ""}
                                </span>
                                <span
                                  style={{
                                    width: 40,
                                    textAlign: "right",
                                    paddingRight: 8,
                                    color: colors.textDim,
                                    backgroundColor: lc.numBg,
                                    userSelect: "none",
                                    fontSize: 11,
                                  }}
                                >
                                  {line.newNum ?? ""}
                                </span>
                                <span
                                  style={{
                                    width: 14,
                                    textAlign: "center",
                                    color: line.type === "add" ? "#86efac" : line.type === "del" ? "#fca5a5" : "transparent",
                                    userSelect: "none",
                                  }}
                                >
                                  {line.type === "add" ? "+" : line.type === "del" ? "−" : " "}
                                </span>
                              </div>
                            );
                          })}
                        </div>
                        {/* Scrollable code area */}
                        <div style={{ flex: 1, overflowX: "auto", minWidth: 0 }}>
                          <div style={{ display: "inline-block", minWidth: "100%" }}>
                            {hunk.lines.map((line, li) => {
                              const lc = lineColors[line.type];
                              return (
                                <div
                                  key={li}
                                  style={{
                                    lineHeight: "20px",
                                    fontFamily: fonts.mono,
                                    fontSize: 12,
                                    whiteSpace: "pre",
                                    color: lc.text,
                                    backgroundColor: lc.bg,
                                    paddingRight: 8,
                                  }}
                                >
                                  {line.content || " "}
                                </div>
                              );
                            })}
                          </div>
                        </div>
                      </div>
                    </div>
                  ))}
                  {file.binary && (
                    <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>
                      Binary file — no content preview
                    </div>
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

const headerBtnStyle: React.CSSProperties = {
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

function hoverIn(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = colors.hoverBg;
  e.currentTarget.style.color = colors.textLight;
}

function hoverOut(e: React.MouseEvent<HTMLButtonElement>) {
  e.currentTarget.style.backgroundColor = "transparent";
  e.currentTarget.style.color = colors.textDim;
}
