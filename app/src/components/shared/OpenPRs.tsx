import { useEffect, useState } from "react";
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
 * failed listing, or with some open. A repo without PRs gets none.
 */
export function showsPRs(prs: PRInfo[] | null, error: string | null): boolean {
  return prs === null || prs.length > 0 || !!error;
}

/**
 * useOpenPRs loads channelId's open PRs (MRs on GitLab) each time enabled
 * turns on: null while loading, with error set when the listing failed.
 */
export function useOpenPRs(channelId: string, enabled: boolean): { prs: PRInfo[] | null; error: string | null } {
  const [prs, setPRs] = useState<PRInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setPRs(null);
    setError(null);
    fetchOpenPRs(channelId)
      .then((list) => {
        if (!cancelled) setPRs(list);
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
  return { prs, error };
}

/**
 * SectionHeader titles a branch picker's section and stays at the top of its
 * scroll area. Its section's scroll area has no top padding, so no row shows
 * above it.
 */
export function SectionHeader({ children }: { children: string }) {
  const { colors } = useTheme();
  return (
    <div
      style={{
        position: "sticky",
        top: 0,
        zIndex: 1,
        padding: "8px 12px 2px",
        backgroundColor: colors.surface,
        fontSize: 10,
        color: colors.textDim,
        textTransform: "uppercase",
        letterSpacing: 1,
      }}
    >
      {children}
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
  search,
  worktrees,
  onPick,
  testId,
}: {
  prs: PRInfo[] | null;
  error: string | null;
  search: string;
  worktrees: WorktreeInfo[] | undefined;
  onPick: (pr: PRInfo, existing?: WorktreeInfo) => void;
  testId: string;
}) {
  const { colors } = useTheme();
  if (!showsPRs(prs, error)) return null;
  const shown = prs?.filter((pr) => matchesPR(pr, search)) ?? [];
  const note = (text: string) => <div style={{ padding: "4px 12px 6px", color: colors.textDim, fontSize: 12, whiteSpace: "pre-wrap" }}>{text}</div>;
  return (
    <div data-testid={testId}>
      <SectionHeader>{prs?.[0]?.kind === "mr" ? "Merge requests" : "Pull requests"}</SectionHeader>
      {!prs && note("Loading…")}
      {error && note(error)}
      {prs && !error && shown.length === 0 && note("No matching PRs")}
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
    </div>
  );
}
