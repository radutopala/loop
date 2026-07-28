import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReviewComment } from "../../api/review";
import { useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { FileLinkOpenDetail } from "../chat/FileLink";
import { ContextMenu } from "../shared/ContextMenu";
import { computeSegments, type HunkLine, type ParsedFile, parseUnifiedDiff } from "./DiffViewer";

interface ReviewDiffViewProps {
  channelId: string;
  rawDiff: string;
  comments: ReviewComment[];
  worktreePath?: string;
  onPushComment: (c: ReviewComment) => void | Promise<void>;
  onPushCommentToChat: (c: ReviewComment) => void | Promise<void>;
  onDeleteComment: (c: ReviewComment) => void | Promise<void>;
}

export interface FileSummary {
  path: string;
  additions: number;
  deletions: number;
  parsed: ParsedFile;
  agentCount: number;
  ghCount: number;
}

// Match comments to a diff line by (path, line, side). Side defaults to
// "RIGHT" both on the comment and on the lookup because the agent
// almost always comments on added/modified lines and the rare LEFT-side
// case must opt in explicitly.
function commentLineSide(c: ReviewComment): "LEFT" | "RIGHT" {
  return c.side === "LEFT" ? "LEFT" : "RIGHT";
}

// Dispatch the same custom event chat-message file links use so an
// editor panel opens the file at the channel's root. The backend
// widens `git diff -U` enough that every commented line lives inside a
// hunk, so we don't need a line target here — clicking a file row just
// opens the file in the editor at the top.
function dispatchOpenFile(channelId: string, relPath: string) {
  const detail: FileLinkOpenDetail = {
    channelId,
    target: { rootIndex: 0, relPath },
    line: null,
  };
  window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
}

// Key a diff line the way FileSection's comment map does, so a comment's
// (line, side) can be located among a hunk's lines. Context lines carry both
// numbers and key as RIGHT, matching where comments render.
function lineKey(line: HunkLine): string {
  if (line.newNum !== null) return `R:${line.newNum}`;
  if (line.oldNum !== null) return `L:${line.oldNum}`;
  return "";
}

/** One comment's position in the rendered scroll, for the floating navigator. */
export interface CommentAnchor {
  id: string;
  /** Owning file, so navigation can expand a collapsed section first. */
  path: string;
  /** Index into `summaries`, or -1 for an orphan. Drives the file rail. */
  fileIdx: number;
}

// Comments in the order they appear on screen: files top-to-bottom, within a
// file by the hunk line each comment anchors to, then orphans last (the
// out-of-diff section renders below every file). Comments whose file is
// collapsed are included — navigation expands the file rather than skipping
// them, so the total doesn't shift under the user as sections open and close.
export function orderedComments(summaries: FileSummary[], byFile: Map<string, ReviewComment[]>, orphans: ReviewComment[]): CommentAnchor[] {
  const out: CommentAnchor[] = [];
  summaries.forEach((sum, fileIdx) => {
    const cs = byFile.get(sum.path);
    if (!cs || cs.length === 0) return;
    // Position of each line key in the flattened hunk list. First occurrence
    // wins: a key repeats only when a line number appears in two hunks, and
    // the comment renders under the first of them.
    const pos = new Map<string, number>();
    let n = 0;
    for (const h of sum.parsed.hunks) {
      for (const l of h.lines) {
        const k = lineKey(l);
        if (k && !pos.has(k)) pos.set(k, n);
        n++;
      }
    }
    // Stable within a line: several comments on one line stack in array
    // order, which is exactly how FileSection renders them. A comment whose
    // line falls outside every hunk is dropped — FileSection has no row to
    // hang it under, so counting it would strand the navigator on a target
    // that never scrolls anywhere.
    const sorted = cs
      .map((c, i) => ({ c, i, at: pos.get(commentLineSide(c) === "LEFT" ? `L:${c.line}` : `R:${c.line}`) }))
      .filter((e): e is { c: ReviewComment; i: number; at: number } => e.at !== undefined)
      .sort((a, b) => a.at - b.at || a.i - b.i);
    for (const { c } of sorted) out.push({ id: c.id, path: sum.path, fileIdx });
  });
  for (const c of orphans) out.push({ id: c.id, path: c.path, fileIdx: -1 });
  return out;
}

function summarize(parsed: ParsedFile, fileComments: ReviewComment[]): FileSummary {
  let additions = 0;
  let deletions = 0;
  for (const h of parsed.hunks) {
    for (const l of h.lines) {
      if (l.type === "add") additions++;
      else if (l.type === "del") deletions++;
    }
  }
  let agentCount = 0;
  let ghCount = 0;
  for (const c of fileComments) {
    if (c.source === "github") ghCount++;
    else agentCount++;
  }
  return { path: parsed.path, additions, deletions, parsed, agentCount, ghCount };
}

export function ReviewDiffView({ channelId, rawDiff, comments, worktreePath, onPushComment, onPushCommentToChat, onDeleteComment }: ReviewDiffViewProps) {
  const { colors } = useTheme();
  const [fileContextMenu, setFileContextMenu] = useState<{ x: number; y: number; path: string } | null>(null);

  const handleFileContextMenu = useCallback((e: React.MouseEvent, path: string) => {
    e.preventDefault();
    setFileContextMenu({ x: e.clientX, y: e.clientY, path });
  }, []);

  const parsedFiles = useMemo(() => parseUnifiedDiff(rawDiff), [rawDiff]);

  // Comments grouped by file path. Comments on files NOT in the diff
  // still need to be visible (e.g. an outdated GH comment whose file was
  // touched on a later force-push). They render under an "Out of diff"
  // pseudo-file at the bottom.
  const { byFile, orphans } = useMemo(() => {
    const knownPaths = new Set(parsedFiles.map((p) => p.path));
    const byFile = new Map<string, ReviewComment[]>();
    const orphans: ReviewComment[] = [];
    for (const c of comments) {
      if (knownPaths.has(c.path)) {
        const arr = byFile.get(c.path) ?? [];
        arr.push(c);
        byFile.set(c.path, arr);
      } else {
        orphans.push(c);
      }
    }
    return { byFile, orphans };
  }, [parsedFiles, comments]);

  const summaries = useMemo(() => parsedFiles.map((p) => summarize(p, byFile.get(p.path) ?? [])), [parsedFiles, byFile]);

  // Files that have any comments start expanded so the user sees
  // existing review context immediately on Load. Other files stay
  // collapsed so the panel doesn't dump the entire patch on screen.
  // The initial value is recomputed when the summary set changes (e.g.
  // after Sync) so newly-touched files with comments auto-expand.
  const [expanded, setExpanded] = useState<Set<string>>(() => initialExpanded(summaries));
  const lastSeedRef = useRef<string>("");
  useEffect(() => {
    const seed = summaries.map((s) => `${s.path}:${s.agentCount + s.ghCount}`).join("|");
    if (seed !== lastSeedRef.current) {
      lastSeedRef.current = seed;
      setExpanded(initialExpanded(summaries));
    }
  }, [summaries]);

  const toggle = useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  const expandAll = useCallback(() => {
    setExpanded(new Set(summaries.map((s) => s.path)));
  }, [summaries]);

  const collapseAll = useCallback(() => {
    setExpanded(new Set());
  }, []);

  // Focus starts on the first file that has any comments, so the user
  // lands on something meaningful right after Load/Sync. Falls back to 0
  // when no file has comments. Re-evaluated when the summaries seed
  // changes (Load/Sync) so a fresh PR resets to the right starting row.
  const [focusedIdx, setFocusedIdx] = useState(() => firstFileWithComments(summaries));
  const focusSeedRef = useRef<string>("");
  useEffect(() => {
    const seed = summaries.map((s) => `${s.path}:${s.agentCount + s.ghCount}`).join("|");
    if (seed !== focusSeedRef.current) {
      focusSeedRef.current = seed;
      setFocusedIdx(firstFileWithComments(summaries));
    }
  }, [summaries]);
  const fileRefs = useRef<Map<number, HTMLDivElement>>(new Map());

  const navigateToFile = useCallback(
    (idx: number) => {
      if (idx < 0 || idx >= summaries.length) return;
      setFocusedIdx(idx);
      const sum = summaries[idx]!;
      setExpanded((prev) => {
        if (prev.has(sum.path)) return prev;
        const next = new Set(prev);
        next.add(sum.path);
        return next;
      });
      requestAnimationFrame(() => {
        fileRefs.current.get(idx)?.scrollIntoView({ behavior: "smooth", block: "start" });
      });
    },
    [summaries],
  );

  // ---- Floating comment navigator ----------------------------------------
  // The toolbar's prev/next steps file-to-file, which is too coarse once a
  // file carries several comments. This walks individual comments instead,
  // and floats over the scroll so it stays reachable at any depth.
  const anchors = useMemo(() => orderedComments(summaries, byFile, orphans), [summaries, byFile, orphans]);
  const commentRefs = useRef<Map<string, HTMLDivElement>>(new Map());
  const scrollRef = useRef<HTMLDivElement>(null);
  const [activeComment, setActiveComment] = useState(0);
  const syncRafRef = useRef(0);

  const registerCommentRef = useCallback((id: string, el: HTMLDivElement | null) => {
    if (el) commentRefs.current.set(id, el);
    else commentRefs.current.delete(id);
  }, []);

  // Which comment the counter reports: the one whose midpoint sits nearest
  // the viewport's midpoint. Measured on scroll rather than tracked with an
  // IntersectionObserver so that navigating (which centres the target) lands
  // on exactly the index we asked for, with no observer/animation race.
  const syncActiveComment = useCallback(() => {
    const sc = scrollRef.current;
    if (!sc) return;
    const r = sc.getBoundingClientRect();
    const mid = r.top + r.height / 2;
    let best = -1;
    let bestDist = Number.POSITIVE_INFINITY;
    anchors.forEach((a, i) => {
      const el = commentRefs.current.get(a.id);
      if (!el) return;
      const er = el.getBoundingClientRect();
      const d = Math.abs(er.top + er.height / 2 - mid);
      if (d < bestDist) {
        bestDist = d;
        best = i;
      }
    });
    // Every comment collapsed out of the DOM: keep the last index rather
    // than snapping the counter to 1.
    if (best >= 0) setActiveComment(best);
  }, [anchors]);

  const onScroll = useCallback(() => {
    if (syncRafRef.current) return;
    syncRafRef.current = requestAnimationFrame(() => {
      syncRafRef.current = 0;
      syncActiveComment();
    });
  }, [syncActiveComment]);

  useEffect(() => {
    return () => {
      if (syncRafRef.current) cancelAnimationFrame(syncRafRef.current);
    };
  }, []);

  const navigateToComment = useCallback(
    (idx: number) => {
      const a = anchors[idx];
      if (!a) return;
      setActiveComment(idx);
      if (a.fileIdx >= 0) setFocusedIdx(a.fileIdx);
      setExpanded((prev) => {
        if (a.fileIdx < 0 || prev.has(a.path)) return prev;
        const next = new Set(prev);
        next.add(a.path);
        return next;
      });
      // Deferred so a just-expanded file has mounted and registered its ref.
      requestAnimationFrame(() => {
        commentRefs.current.get(a.id)?.scrollIntoView({ behavior: "smooth", block: "center" });
      });
    },
    [anchors],
  );

  const clampedIdx = Math.min(focusedIdx, Math.max(summaries.length - 1, 0));
  const focusedPath = summaries[clampedIdx]?.path ?? "";

  // Prev/Next jump only between files that carry comments — uncommented
  // files (large, refactored, generated, etc.) are usually not what a
  // reviewer wants to flip through. The denominator includes ALL files
  // with any comment regardless of source: agent-emitted from the
  // current run, plus GH comments loaded on Load or refreshed on Sync.
  // We also fold in unique orphan paths (GH comments whose file is no
  // longer in the diff) so "n / m commented" reflects every commented
  // entity the user can see, not just what's nav-able.
  const commentedIndices = useMemo(() => summaries.flatMap((s, i) => (s.agentCount + s.ghCount > 0 ? [i] : [])), [summaries]);
  const orphanPathCount = useMemo(() => {
    const paths = new Set<string>();
    for (const c of orphans) paths.add(c.path);
    return paths.size;
  }, [orphans]);
  const prevCommentedIdx = useMemo(() => {
    for (let i = commentedIndices.length - 1; i >= 0; i--) {
      if (commentedIndices[i]! < clampedIdx) return commentedIndices[i]!;
    }
    return -1;
  }, [commentedIndices, clampedIdx]);
  const nextCommentedIdx = useMemo(() => {
    for (const i of commentedIndices) {
      if (i > clampedIdx) return i;
    }
    return -1;
  }, [commentedIndices, clampedIdx]);

  // Every hook must run before this bail-out: the branch flips when the first
  // out-of-diff comment lands on an empty session, and React counts hooks per
  // render. The four memos above used to sit below it.
  if (parsedFiles.length === 0 && comments.length === 0) {
    return <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>No diff content. The PR may be empty or the worktree failed to load.</div>;
  }

  // Comments stream in while the agent reviews, so the index can outrun the
  // list on a Sync that drops rows.
  const commentIdx = Math.min(activeComment, Math.max(anchors.length - 1, 0));
  const totalCommented = commentedIndices.length + orphanPathCount;
  const positionInCommented = commentedIndices.indexOf(clampedIdx);

  return (
    <div style={{ display: "flex", flexDirection: "column", flex: 1, minHeight: 0, position: "relative" }}>
      {summaries.length > 0 && (
        <DiffToolbar
          colors={colors}
          focusedPath={focusedPath}
          index={positionInCommented >= 0 ? positionInCommented : -1}
          total={totalCommented}
          canPrev={prevCommentedIdx >= 0}
          canNext={nextCommentedIdx >= 0}
          onExpandAll={expandAll}
          onCollapseAll={collapseAll}
          onPrev={() => navigateToFile(prevCommentedIdx)}
          onNext={() => navigateToFile(nextCommentedIdx)}
        />
      )}
      <div ref={scrollRef} onScroll={onScroll} style={{ flex: 1, overflow: "auto", minHeight: 0 }}>
        {summaries.map((sum, idx) => (
          <div
            key={sum.path}
            ref={(el) => {
              if (el) fileRefs.current.set(idx, el);
              else fileRefs.current.delete(idx);
            }}
            style={{ borderLeft: `3px solid ${idx === clampedIdx ? colors.active : "transparent"}`, transition: "border-color 0.15s ease" }}
          >
            <FileSection
              summary={sum}
              comments={byFile.get(sum.path) ?? []}
              expanded={expanded.has(sum.path)}
              colors={colors}
              onToggle={() => {
                setFocusedIdx(idx);
                toggle(sum.path);
              }}
              onContextMenu={handleFileContextMenu}
              onPushComment={onPushComment}
              onPushCommentToChat={onPushCommentToChat}
              onDeleteComment={onDeleteComment}
              registerCommentRef={registerCommentRef}
            />
          </div>
        ))}
        {orphans.length > 0 && (
          <OrphanCommentsSection
            comments={orphans}
            colors={colors}
            onContextMenu={handleFileContextMenu}
            onPushComment={onPushComment}
            onPushCommentToChat={onPushCommentToChat}
            onDeleteComment={onDeleteComment}
            registerCommentRef={registerCommentRef}
          />
        )}
      </div>
      {anchors.length > 0 && (
        <CommentNavigator colors={colors} index={commentIdx} total={anchors.length} onPrev={() => navigateToComment(commentIdx - 1)} onNext={() => navigateToComment(commentIdx + 1)} />
      )}
      {fileContextMenu && (
        <ContextMenu
          x={fileContextMenu.x}
          y={fileContextMenu.y}
          items={[
            { label: "Open file", onClick: () => dispatchOpenFile(channelId, fileContextMenu.path) },
            { label: "Copy relative path", onClick: () => void navigator.clipboard.writeText(fileContextMenu.path) },
            { label: "Copy absolute path", onClick: () => void navigator.clipboard.writeText((worktreePath ?? "") + "/" + fileContextMenu.path) },
          ]}
          onClose={() => setFileContextMenu(null)}
        />
      )}
    </div>
  );
}

function initialExpanded(summaries: FileSummary[]): Set<string> {
  const initial = new Set<string>();
  for (const sum of summaries) {
    if (sum.agentCount + sum.ghCount > 0) initial.add(sum.path);
  }
  return initial;
}

function firstFileWithComments(summaries: FileSummary[]): number {
  for (let i = 0; i < summaries.length; i++) {
    const s = summaries[i]!;
    if (s.agentCount + s.ghCount > 0) return i;
  }
  return 0;
}

// Floating prev/next over the diff scroll. Stepping a comment at a time is
// what a reviewer actually wants once a file carries several of them — the
// toolbar's pair only moves file-to-file. Pinned bottom-right so it clears
// the inline comment cards, which are indented from the left gutter.
function CommentNavigator({ colors, index, total, onPrev, onNext }: { colors: ColorPalette; index: number; total: number; onPrev: () => void; onNext: () => void }) {
  const canPrev = index > 0;
  const canNext = index < total - 1;
  const btn = (enabled: boolean): React.CSSProperties => ({
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: 22,
    height: 22,
    border: "none",
    borderRadius: 11,
    background: "transparent",
    color: colors.textLight,
    cursor: enabled ? "pointer" : "default",
    opacity: enabled ? 1 : 0.3,
    padding: 0,
  });
  return (
    <div
      data-testid="review-comment-nav"
      style={{
        position: "absolute",
        right: 14,
        bottom: 14,
        display: "flex",
        alignItems: "center",
        gap: 2,
        padding: "3px 6px",
        borderRadius: 14,
        border: `1px solid ${colors.border}`,
        background: colors.surface,
        boxShadow: `0 2px 10px ${colors.shadow}`,
        // Above the diff rows, below ContextMenu (999/1000).
        zIndex: 5,
      }}
    >
      <button data-testid="review-comment-nav-prev" style={btn(canPrev)} disabled={!canPrev} onClick={onPrev} title="Previous comment" aria-label="Previous comment">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2.5 6.5L5 3.5L7.5 6.5" />
        </svg>
      </button>
      <span style={{ fontFamily: fonts.mono, fontSize: 11, color: colors.textDim, minWidth: 52, textAlign: "center", userSelect: "none" }} title={`Comment ${index + 1} of ${total}`}>
        {index + 1} / {total}
      </span>
      <button data-testid="review-comment-nav-next" style={btn(canNext)} disabled={!canNext} onClick={onNext} title="Next comment" aria-label="Next comment">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2.5 3.5L5 6.5L7.5 3.5" />
        </svg>
      </button>
    </div>
  );
}

function DiffToolbar({
  colors,
  focusedPath,
  index,
  total,
  canPrev,
  canNext,
  onExpandAll,
  onCollapseAll,
  onPrev,
  onNext,
}: {
  colors: ColorPalette;
  focusedPath: string;
  index: number;
  total: number;
  canPrev: boolean;
  canNext: boolean;
  onExpandAll: () => void;
  onCollapseAll: () => void;
  onPrev: () => void;
  onNext: () => void;
}) {
  const btn: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    width: 24,
    height: 24,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    background: "transparent",
    color: colors.textMuted,
    cursor: "pointer",
    fontSize: 14,
    lineHeight: 1,
  };
  return (
    <div
      data-testid="review-diff-toolbar"
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        padding: "4px 12px",
        background: colors.surface,
        borderBottom: `1px solid ${colors.border}`,
        minHeight: 32,
        flexShrink: 0,
      }}
    >
      <button data-testid="review-diff-expand-all" onClick={onExpandAll} title="Expand all" style={btn}>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="7,8 12,13 17,8" />
          <polyline points="7,14 12,19 17,14" />
        </svg>
      </button>
      <button data-testid="review-diff-collapse-all" onClick={onCollapseAll} title="Collapse all" style={btn}>
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="7,14 12,9 17,14" />
          <polyline points="7,20 12,15 17,20" />
        </svg>
      </button>
      <button data-testid="review-diff-prev-file" style={{ ...btn, opacity: canPrev ? 1 : 0.3, cursor: canPrev ? "pointer" : "default" }} disabled={!canPrev} onClick={onPrev} title="Previous file">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2.5 6.5L5 3.5L7.5 6.5" />
        </svg>
      </button>
      <button data-testid="review-diff-next-file" style={{ ...btn, opacity: canNext ? 1 : 0.3, cursor: canNext ? "pointer" : "default" }} disabled={!canNext} onClick={onNext} title="Next file">
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M2.5 3.5L5 6.5L7.5 3.5" />
        </svg>
      </button>
      <span
        style={{
          flex: 1,
          fontFamily: fonts.mono,
          fontSize: 12,
          color: colors.textLight,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {focusedPath}
      </span>
      <span style={{ fontFamily: fonts.mono, fontSize: 11, color: colors.textDim, flexShrink: 0 }}>
        {total === 0 ? "0 commented" : index >= 0 ? `${index + 1} / ${total} commented` : `– / ${total} commented`}
      </span>
    </div>
  );
}

function FileSection({
  summary,
  comments,
  expanded,
  colors,
  onToggle,
  onContextMenu,
  onPushComment,
  onPushCommentToChat,
  onDeleteComment,
  registerCommentRef,
}: {
  summary: FileSummary;
  comments: ReviewComment[];
  expanded: boolean;
  colors: ColorPalette;
  onToggle: () => void;
  onContextMenu: (e: React.MouseEvent, path: string) => void;
  onPushComment: (c: ReviewComment) => void | Promise<void>;
  onPushCommentToChat: (c: ReviewComment) => void | Promise<void>;
  onDeleteComment: (c: ReviewComment) => void | Promise<void>;
  registerCommentRef: (id: string, el: HTMLDivElement | null) => void;
}) {
  // Group comments by (line, side) so multiple comments on the same line
  // render as a stack underneath that line. The backend widens git's
  // `-U` enough to land every comment on a hunk line, so we don't need
  // a separate out-of-hunk path here.
  const commentMap = new Map<string, ReviewComment[]>();
  for (const c of comments) {
    const k = commentLineSide(c) === "LEFT" ? `L:${c.line}` : `R:${c.line}`;
    const arr = commentMap.get(k) ?? [];
    arr.push(c);
    commentMap.set(k, arr);
  }

  const segments = computeSegments(summary.parsed);
  const totalCount = summary.agentCount + summary.ghCount;

  return (
    <div data-testid={`review-diff-file-${summary.path}`}>
      <button
        onClick={onToggle}
        onContextMenu={(e) => onContextMenu(e, summary.path)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          width: "100%",
          padding: "4px 12px",
          border: "none",
          background: expanded ? colors.hoverBg : "transparent",
          borderBottom: `1px solid ${colors.border}`,
          color: colors.textLight,
          fontSize: 12,
          fontFamily: fonts.mono,
          textAlign: "left",
          cursor: "pointer",
        }}
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
          <bdi>{summary.path}</bdi>
        </span>
        {totalCount > 0 && (
          <span
            data-testid={`review-diff-file-count-${summary.path}`}
            style={{
              flexShrink: 0,
              fontSize: 10,
              padding: "1px 6px",
              borderRadius: 3,
              border: `1px solid ${colors.active}`,
              color: colors.active,
            }}
            title={`${summary.agentCount} agent · ${summary.ghCount} github`}
          >
            {totalCount}
          </span>
        )}
        <span style={{ flexShrink: 0, width: 80, fontSize: 11, textAlign: "right" }}>
          <span style={{ color: colors.diffAddText }}>+{summary.additions}</span> <span style={{ color: colors.diffDelText }}>-{summary.deletions}</span>
        </span>
      </button>
      {expanded && (
        <div style={{ borderBottom: `1px solid ${colors.border}` }}>
          {segments.map((seg, si) => {
            if (seg.kind !== "hunk") return null;
            return (
              <div key={si}>
                <div
                  style={{
                    padding: "2px 12px",
                    fontSize: 11,
                    fontFamily: fonts.mono,
                    color: colors.textDim,
                    backgroundColor: colors.diffHunkBg,
                    whiteSpace: "pre",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {seg.hunk.header}
                </div>
                {seg.hunk.lines.map((line, li) => {
                  const key = lineKey(line);
                  const matched = key ? commentMap.get(key) : undefined;
                  return (
                    <div key={li}>
                      <DiffLineRow line={line} colors={colors} />
                      {matched &&
                        matched.map((c) => (
                          <InlineComment key={c.id} comment={c} colors={colors} onPush={onPushComment} onPushToChat={onPushCommentToChat} onDelete={onDeleteComment} registerRef={registerCommentRef} />
                        ))}
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function DiffLineRow({ line, colors }: { line: HunkLine; colors: ColorPalette }) {
  const lineColors = {
    add: { bg: colors.diffAddBg, numBg: colors.diffAddNumBg, text: colors.diffAddText },
    del: { bg: colors.diffDelBg, numBg: colors.diffDelNumBg, text: colors.diffDelText },
    ctx: { bg: "transparent", numBg: "transparent", text: colors.textMuted },
  } as const;
  const lc = lineColors[line.type];
  return (
    <div style={{ display: "flex", lineHeight: "20px", fontFamily: fonts.mono, backgroundColor: lc.bg }}>
      <span
        style={{
          width: 40,
          textAlign: "right",
          paddingRight: 4,
          color: colors.textDim,
          backgroundColor: lc.numBg,
          userSelect: "none",
          fontSize: 11,
          flexShrink: 0,
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
          flexShrink: 0,
        }}
      >
        {line.newNum ?? ""}
      </span>
      <span
        style={{
          width: 14,
          textAlign: "center",
          color: line.type === "add" ? colors.diffAddText : line.type === "del" ? colors.diffDelText : "transparent",
          userSelect: "none",
          flexShrink: 0,
        }}
      >
        {line.type === "add" ? "+" : line.type === "del" ? "−" : " "}
      </span>
      <span
        style={{
          flex: 1,
          fontSize: 12,
          whiteSpace: "pre",
          color: lc.text,
          paddingRight: 8,
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {line.content || " "}
      </span>
    </div>
  );
}

function InlineComment({
  comment,
  colors,
  onPush,
  onPushToChat,
  onDelete,
  registerRef,
}: {
  comment: ReviewComment;
  colors: ColorPalette;
  onPush: (c: ReviewComment) => void | Promise<void>;
  onPushToChat: (c: ReviewComment) => void | Promise<void>;
  onDelete: (c: ReviewComment) => void | Promise<void>;
  /** Hands the card's node to the floating navigator so it can scroll to it. */
  registerRef: (id: string, el: HTMLDivElement | null) => void;
}) {
  // Local in-flight flag for the "Push to chat" button. Push-to-chat
  // doesn't flip the comment to `pushed`, so without this guard rapid
  // double-clicks during the sendMessage round-trip would queue
  // duplicate prompts to the agent.
  const [sendingChat, setSendingChat] = useState(false);
  const handlePushToChat = async () => {
    if (sendingChat) return;
    setSendingChat(true);
    try {
      await onPushToChat(comment);
    } finally {
      setSendingChat(false);
    }
  };
  const isGitHub = comment.source === "github";
  const headerLabel = isGitHub ? (comment.author ? `@${comment.author}` : "github") : "agent";
  return (
    <div
      data-testid={`review-comment-${comment.id}`}
      ref={(el) => registerRef(comment.id, el)}
      style={{
        margin: "4px 8px 4px 88px",
        padding: "6px 10px",
        borderLeft: `3px solid ${isGitHub ? colors.textDim : colors.active}`,
        background: colors.surface,
        borderRadius: 3,
        display: "flex",
        flexDirection: "column",
        gap: 4,
        // De-emphasize resolved threads so the user's eye lands on what
        // still needs attention. Fully hiding them would obscure history;
        // 50% is enough to make them recede without losing context.
        opacity: comment.resolved ? 0.55 : 1,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 11 }}>
        <span
          style={{
            color: isGitHub ? colors.textDim : colors.active,
            fontFamily: fonts.sans,
            fontWeight: 600,
          }}
        >
          {headerLabel}
        </span>
        {comment.outdated && (
          <span
            style={{
              fontSize: 9,
              padding: "0 4px",
              borderRadius: 3,
              border: `1px solid ${colors.textDim}`,
              color: colors.textDim,
              textTransform: "uppercase",
            }}
            title="GitHub couldn't anchor this comment to the current head"
          >
            outdated
          </span>
        )}
        {comment.resolved && (
          <span
            data-testid={`review-comment-resolved-${comment.id}`}
            style={{
              fontSize: 9,
              padding: "0 4px",
              borderRadius: 3,
              border: `1px solid ${colors.active}`,
              color: colors.active,
              textTransform: "uppercase",
            }}
            title="This review thread is resolved on GitHub"
          >
            resolved
          </span>
        )}
        <span style={{ flex: 1 }} />
        {comment.url && (
          <a href={comment.url} target="_blank" rel="noreferrer noopener" style={{ fontSize: 10, color: colors.textDim, textDecoration: "none" }} title="Open on GitHub">
            view
          </a>
        )}
        {comment.pushed ? (
          <span style={{ fontSize: 10, color: colors.textDim }}>{isGitHub ? "on github" : "pushed"}</span>
        ) : (
          <>
            <button
              data-testid={`review-comment-push-chat-${comment.id}`}
              onClick={() => void handlePushToChat()}
              disabled={sendingChat}
              style={{
                background: "transparent",
                color: colors.text,
                border: `1px solid ${colors.border}`,
                borderRadius: 3,
                padding: "1px 6px",
                fontSize: 10,
                fontFamily: fonts.sans,
                cursor: sendingChat ? "not-allowed" : "pointer",
                opacity: sendingChat ? 0.5 : 1,
              }}
              title="Send this comment to the chat as a prompt for the agent"
            >
              {sendingChat ? "Sending..." : "Push to chat"}
            </button>
            <button
              data-testid={`review-comment-push-${comment.id}`}
              onClick={() => void onPush(comment)}
              style={{
                background: "transparent",
                color: colors.text,
                border: `1px solid ${colors.border}`,
                borderRadius: 3,
                padding: "1px 6px",
                fontSize: 10,
                fontFamily: fonts.sans,
                cursor: "pointer",
              }}
              title="Push this comment to GitHub"
            >
              Push to GitHub
            </button>
          </>
        )}
        <button
          data-testid={`review-comment-delete-${comment.id}`}
          onClick={() => void onDelete(comment)}
          style={{
            background: "transparent",
            color: colors.textDim,
            border: `1px solid ${colors.border}`,
            borderRadius: 3,
            padding: "1px 6px",
            fontSize: 10,
            fontFamily: fonts.sans,
            cursor: "pointer",
          }}
          title={comment.github_id ? "Delete this comment from GitHub" : "Discard this comment"}
        >
          Delete
        </button>
      </div>
      <div
        style={{
          fontSize: 12,
          color: colors.text,
          whiteSpace: "pre-wrap",
          fontFamily: fonts.sans,
        }}
      >
        {comment.body}
      </div>
    </div>
  );
}

// OrphanCommentsSection renders comments whose path doesn't appear in
// the current diff. The most common cause is a GH comment from a prior
// commit on a file no longer touched by the PR (e.g. reverted in a
// later push). Rather than dropping them we render a compact list so
// the user is aware they exist.
function OrphanCommentsSection({
  comments,
  colors,
  onContextMenu,
  onPushComment,
  onPushCommentToChat,
  onDeleteComment,
  registerCommentRef,
}: {
  comments: ReviewComment[];
  colors: ColorPalette;
  onContextMenu: (e: React.MouseEvent, path: string) => void;
  onPushComment: (c: ReviewComment) => void | Promise<void>;
  onPushCommentToChat: (c: ReviewComment) => void | Promise<void>;
  onDeleteComment: (c: ReviewComment) => void | Promise<void>;
  registerCommentRef: (id: string, el: HTMLDivElement | null) => void;
}) {
  return (
    <div data-testid="review-diff-orphans">
      <div
        style={{
          padding: "4px 12px",
          fontSize: 11,
          color: colors.textDim,
          backgroundColor: colors.diffHunkBg,
          borderTop: `1px solid ${colors.border}`,
          borderBottom: `1px solid ${colors.border}`,
        }}
      >
        Comments outside the current diff ({comments.length})
      </div>
      {comments.map((c) => (
        <div key={c.id} style={{ padding: "0 8px" }}>
          <div
            onContextMenu={(e) => onContextMenu(e, c.path)}
            style={{
              fontSize: 11,
              color: colors.textDim,
              fontFamily: fonts.mono,
              padding: "4px 0 0 0",
            }}
          >
            {c.path}:{c.line}
          </div>
          <InlineComment comment={c} colors={colors} onPush={onPushComment} onPushToChat={onPushCommentToChat} onDelete={onDeleteComment} registerRef={registerCommentRef} />
        </div>
      ))}
    </div>
  );
}
