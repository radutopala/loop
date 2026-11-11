import type { WorktreeInfo } from "../../api/git";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { SectionHeader } from "./OpenPRs";

/** The worktrees whose branch or directory name holds search, ignoring case. */
export function filterWorktrees(worktrees: readonly WorktreeInfo[], search: string): WorktreeInfo[] {
  const q = search.trim().toLowerCase();
  if (!q) return [...worktrees];
  return worktrees.filter((wt) => wt.branch.toLowerCase().includes(q) || (wt.path.split("/").pop() ?? "").toLowerCase().includes(q));
}

/**
 * The Worktrees section of a branch picker: the repo's worktrees, matching
 * search, each with "go" to its thread or, without one, "imp" to import it
 * as a thread. onDone closes the picker before either runs.
 */
export function WorktreeRows({
  testId,
  worktrees,
  search,
  onSelectThread,
  onImport,
  onDone,
}: {
  testId?: string;
  worktrees: readonly WorktreeInfo[];
  search: string;
  onSelectThread?: (threadId: string) => void;
  onImport?: (worktreePath: string) => void;
  onDone: () => void;
}) {
  const { colors } = useTheme();
  const shown = filterWorktrees(worktrees, search);
  const action = {
    padding: "2px 6px",
    border: "none",
    background: "transparent",
    cursor: "pointer",
    borderRadius: 4,
    flexShrink: 0,
    fontSize: 10,
    fontFamily: fonts.mono,
  } as const;

  return (
    <div data-testid={testId}>
      <SectionHeader>Worktrees</SectionHeader>
      {shown.map((wt) => {
        const dirName = wt.path.split("/").pop() || wt.path;
        return (
          <div
            key={wt.path}
            data-testid="worktree-row"
            style={{ display: "flex", alignItems: "center", borderRadius: 4 }}
            onMouseEnter={(e) => {
              e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)";
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.backgroundColor = "transparent";
            }}
          >
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                flex: 1,
                padding: "5px 0 5px 12px",
                fontSize: 12,
                fontFamily: fonts.mono,
                whiteSpace: "nowrap",
                overflow: "hidden",
                minWidth: 0,
              }}
            >
              {/* Folder icon */}
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
              </svg>
              <span style={{ color: colors.text, overflow: "hidden", textOverflow: "ellipsis" }}>{wt.branch || "(detached)"}</span>
              <span style={{ color: colors.textDim, fontSize: 10, flexShrink: 0 }}>{dirName}</span>
            </div>
            {wt.thread_id && onSelectThread ? (
              <button
                data-testid="worktree-go"
                onClick={(e) => {
                  e.stopPropagation();
                  onDone();
                  onSelectThread(wt.thread_id!);
                }}
                title="Go to thread"
                style={{ ...action, color: colors.active }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.opacity = "0.7";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.opacity = "1";
                }}
              >
                go
              </button>
            ) : onImport ? (
              <button
                data-testid="worktree-import"
                onClick={(e) => {
                  e.stopPropagation();
                  onDone();
                  onImport(wt.path);
                }}
                title="Import worktree as thread"
                style={{ ...action, color: colors.textDim }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.color = colors.active;
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.color = colors.textDim;
                }}
              >
                imp
              </button>
            ) : null}
          </div>
        );
      })}
      {shown.length === 0 && <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No worktrees found</div>}
    </div>
  );
}
