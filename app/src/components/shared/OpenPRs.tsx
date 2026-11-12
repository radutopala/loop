import { type ReactNode, useEffect, useState } from "react";
import { fetchOpenPRs, type PRInfo, type WorktreeInfo } from "../../api/git";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

/**
 * OpenPRHandler opens pr of channelId's repo: in existing, the worktree
 * already on its branch, when there is one, else in a new worktree thread
 * checked out on its branch.
 */
export type OpenPRHandler = (channelId: string, pr: PRInfo, existing?: WorktreeInfo) => void | Promise<void>;

/** prRef is how the forge writes pr's number: !7 for an MR, else #12. */
export function prRef(pr: PRInfo): string {
  return `${pr.kind === "mr" ? "!" : "#"}${pr.number}`;
}

/** matchesPR reports whether search (any case) is in pr's ref, title or branch. */
export function matchesPR(pr: PRInfo, search: string): boolean {
  const s = search.trim().toLowerCase();
  if (!s) return true;
  return [prRef(pr), String(pr.number), pr.title ?? "", pr.head_ref].some((v) => v.toLowerCase().includes(s));
}

/** prWorktree is the worktree already on pr's branch, if any. */
export function prWorktree(pr: PRInfo, worktrees: WorktreeInfo[] | undefined): WorktreeInfo | undefined {
  return worktrees?.find((wt) => wt.branch === pr.head_ref);
}

/** prAction is what picking pr does, as its button's label and tooltip. */
export function prAction(pr: PRInfo, existing: WorktreeInfo | undefined): { label: string; title: string } {
  if (existing?.thread_id) return { label: "go", title: `Go to the worktree thread on ${pr.head_ref}` };
  if (existing) return { label: "imp", title: `Import the worktree on ${pr.head_ref} as a thread` };
  return { label: "+wt", title: `Check ${prRef(pr)} out on ${pr.head_ref} in a new worktree thread` };
}

/**
 * showsPRs reports whether a picker has a PR section: while loading, on a
 * failed listing, with some open, or for a repo on a forge with none open.
 * A repo without a forge to list PRs from gets none.
 */
export function showsPRs(prs: PRInfo[] | null, error: string | null, forge?: string): boolean {
  return prs === null || prs.length > 0 || !!error || !!forge;
}

/** emptyPRsNote is a PR section's note when nothing shows: none open in the
 *  repo, or none matching the search. */
export function emptyPRsNote(prs: PRInfo[], forge?: string): string {
  if (prs.length > 0) return "No matching PRs";
  return forge === "gitlab" ? "No open merge requests" : "No open pull requests";
}

/**
 * useOpenPRs loads channelId's open PRs (MRs on GitLab) each time enabled
 * turns on: null while loading, with error set when the listing failed.
 */
export function useOpenPRs(channelId: string, enabled: boolean): { prs: PRInfo[] | null; error: string | null; forge?: string } {
  const [prs, setPRs] = useState<PRInfo[] | null>(null);
  const [forge, setForge] = useState<string | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setPRs(null);
    setForge(undefined);
    setError(null);
    fetchOpenPRs(channelId)
      .then((listed) => {
        if (cancelled) return;
        setPRs(listed.prs);
        setForge(listed.forge);
      })
      .catch((err) => {
        if (cancelled) return;
        setPRs([]);
        setError(err instanceof Error ? err.message.trim() : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [channelId, enabled]);
  return { prs, error, forge };
}

/**
 * PickerSection is one section of a branch picker: a full-width title band
 * over the section's own scroll area (just the area, without a title). divided
 * draws a line above it, for a section below another.
 */
export function PickerSection({ title, testId, divided, children }: { title?: string; testId?: string; divided?: boolean; children: ReactNode }) {
  const { colors } = useTheme();
  return (
    <div data-testid={testId} style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", borderTop: divided ? `1px solid ${colors.border}` : undefined }}>
      {title && (
        <div
          style={{
            flexShrink: 0,
            padding: "5px 12px",
            backgroundColor: colors.bg,
            borderBottom: `1px solid ${colors.border}`,
            fontSize: 10,
            fontWeight: 600,
            color: colors.textMuted,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          {title}
        </div>
      )}
      <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: title ? "2px 0 4px" : "4px 0" }}>{children}</div>
    </div>
  );
}

/**
 * OpenPRRows is a branch picker's section of open PRs, filtered by search;
 * nothing once loaded when the repo has none. Picking one calls onPick with
 * the worktree already on its branch, if any.
 */
export function OpenPRRows({
  prs,
  error,
  forge,
  search,
  worktrees,
  onPick,
  testId,
}: {
  prs: PRInfo[] | null;
  error: string | null;
  /** The repo's forge, for its section title and note when it has none open. */
  forge?: string;
  search: string;
  worktrees: WorktreeInfo[] | undefined;
  onPick: (pr: PRInfo, existing?: WorktreeInfo) => void;
  testId: string;
}) {
  const { colors } = useTheme();
  if (!showsPRs(prs, error, forge)) return null;
  const shown = prs?.filter((pr) => matchesPR(pr, search)) ?? [];
  const note = (text: string) => <div style={{ padding: "4px 12px 6px", color: colors.textDim, fontSize: 12, whiteSpace: "pre-wrap" }}>{text}</div>;
  return (
    <PickerSection testId={testId} title={prs?.[0]?.kind === "mr" || forge === "gitlab" ? "Merge requests" : "Pull requests"} divided>
      {!prs && note("Loading…")}
      {error && note(error)}
      {prs && !error && shown.length === 0 && note(emptyPRsNote(prs, forge))}
      {shown.map((pr) => {
        const existing = prWorktree(pr, worktrees);
        const action = prAction(pr, existing);
        return (
          <button
            key={pr.number}
            data-testid={`${testId}-${pr.number}`}
            onClick={() => onPick(pr, existing)}
            title={action.title}
            style={{
              display: "flex",
              alignItems: "baseline",
              gap: 6,
              width: "100%",
              padding: "5px 12px",
              border: "none",
              background: "transparent",
              color: colors.text,
              cursor: "pointer",
              fontSize: 12,
              fontFamily: fonts.sans,
              textAlign: "left",
              whiteSpace: "nowrap",
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)";
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.backgroundColor = "transparent";
            }}
          >
            <span style={{ color: colors.textDim, fontFamily: fonts.mono, flexShrink: 0 }}>{prRef(pr)}</span>
            <span style={{ display: "flex", flexDirection: "column", flex: 1, minWidth: 0 }}>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>
                {pr.is_draft && <span style={{ color: colors.textDim }}>draft · </span>}
                {pr.title || pr.head_ref}
              </span>
              <span style={{ color: colors.textDim, fontFamily: fonts.mono, fontSize: 10, overflow: "hidden", textOverflow: "ellipsis" }}>{pr.head_ref}</span>
            </span>
            <span style={{ color: existing?.thread_id ? colors.active : colors.textDim, fontFamily: fonts.mono, fontSize: 10, flexShrink: 0 }}>{action.label}</span>
          </button>
        );
      })}
    </PickerSection>
  );
}
