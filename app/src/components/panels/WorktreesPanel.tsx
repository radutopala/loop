import { useCallback, useEffect, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { useEventStream } from "../../hooks/useEventStream";
import { fetchBranches, type WorktreeInfo } from "../../api/git";
import { removeWorktree } from "../../api/channels";
import { fonts } from "../../theme";

interface WorktreesPanelProps {
  channelId: string;
  isWorktree: boolean;
  hasBranch: boolean;
  onImportWorktree?: (channelId: string, worktreePath: string) => Promise<void>;
  onSelectThread?: (threadId: string) => void;
}

export function WorktreesPanel({ channelId, isWorktree, hasBranch, onImportWorktree, onSelectThread }: WorktreesPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [worktrees, setWorktrees] = useState<WorktreeInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const [removingPath, setRemovingPath] = useState<string | null>(null);
  const [confirmingPath, setConfirmingPath] = useState<string | null>(null);
  const [importingPath, setImportingPath] = useState<string | null>(null);
  // Close confirm popover on outside click.
  useEffect(() => {
    if (!confirmingId && !confirmingPath) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest("[data-confirm-popover]")) {
        setConfirmingId(null);
        setConfirmingPath(null);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirmingId, confirmingPath]);

  const loadWorktrees = useCallback(async () => {
    try {
      const info = await fetchBranches(channelId);
      setWorktrees(info.worktrees ?? []);
    } catch {
      setWorktrees([]);
    } finally {
      setLoading(false);
    }
  }, [channelId]);

  useEffect(() => {
    loadWorktrees();
  }, [loadWorktrees]);

  const onEvent = useCallback(
    (event: { type: string }) => {
      if (event.type === "channel.created" || event.type === "channel.deleted") {
        loadWorktrees();
      }
    },
    [loadWorktrees],
  );

  useEventStream({ channelId, onEvent });

  const handleDelete = async (wt: WorktreeInfo) => {
    if (!wt.thread_id) return;
    setDeletingId(wt.thread_id);
    try {
      await removeWorktree(channelId, wt.path, wt.thread_id);
      await loadWorktrees();
    } catch {
      // ignore
    } finally {
      setDeletingId(null);
    }
  };

  const handleRemove = async (wt: WorktreeInfo) => {
    setRemovingPath(wt.path);
    try {
      await removeWorktree(channelId, wt.path);
      await loadWorktrees();
    } catch {
      // ignore
    } finally {
      setRemovingPath(null);
    }
  };

  const handleImport = async (wt: WorktreeInfo) => {
    if (!onImportWorktree) return;
    setImportingPath(wt.path);
    try {
      await onImportWorktree(channelId, wt.path);
      await loadWorktrees();
    } catch {
      // ignore
    } finally {
      setImportingPath(null);
    }
  };

  const handleNavigate = (wt: WorktreeInfo) => {
    if (wt.thread_id && onSelectThread) {
      onSelectThread(wt.thread_id);
    }
  };

  if (isWorktree) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>
        Worktrees are managed from the parent channel
      </div>
    );
  }

  if (!hasBranch) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>
        No git repository detected
      </div>
    );
  }

  if (loading) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>
        Loading...
      </div>
    );
  }

  if (worktrees.length === 0) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>
        No worktrees
      </div>
    );
  }

  const btnStyle: React.CSSProperties = {
    padding: "2px 8px",
    minWidth: 48,
    fontSize: 10,
    lineHeight: 1.4,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    background: "transparent",
    color: colors.textDim,
    cursor: "pointer",
    textAlign: "center",
  };

  return (
    <div data-testid="worktrees-panel" style={{ flex: 1, overflow: "auto", padding: 12 }}>
      {worktrees.map((wt) => {
        const basename = wt.path.split("/").pop() || wt.path;
        const imported = !!wt.thread_id;
        const isDeleting = deletingId === wt.thread_id;
        const isImporting = importingPath === wt.path;

        return (
          <div
            key={wt.path}
            style={{
              position: "relative",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              padding: "8px 10px",
              borderBottom: `1px solid ${colors.border}`,
            }}
          >
            <div style={{ minWidth: 0, flex: 1 }}>
              <div style={{ fontSize: fontSizes.panels, color: colors.textLight, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
                {wt.branch || "detached"}
              </div>
              <div style={{ fontSize: 10, color: colors.textDim, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
                {basename}
              </div>
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginLeft: 12, flexShrink: 0 }}>
              {imported ? (
                <>
                  <button style={btnStyle} onClick={() => handleNavigate(wt)} title="Open worktree thread">
                    Go
                  </button>
                  <button
                    style={{ ...btnStyle, color: isDeleting ? colors.textDim : "#ef4444" }}
                    onClick={() => setConfirmingId(wt.thread_id!)}
                    disabled={isDeleting}
                    title="Remove worktree from disk and delete thread"
                  >
                    {isDeleting ? "..." : "Delete"}
                  </button>
                </>
              ) : (
                <>
                  <button
                    style={btnStyle}
                    onClick={() => handleImport(wt)}
                    disabled={isImporting}
                    title="Import worktree as thread"
                  >
                    {isImporting ? "..." : "Import"}
                  </button>
                  <button
                    style={{ ...btnStyle, color: removingPath === wt.path ? colors.textDim : "#ef4444" }}
                    onClick={() => setConfirmingPath(wt.path)}
                    disabled={removingPath === wt.path}
                    title="Remove worktree from disk"
                  >
                    {removingPath === wt.path ? "..." : "Delete"}
                  </button>
                </>
              )}
            </div>
            {(confirmingId === wt.thread_id || confirmingPath === wt.path) && (
              <div
                data-confirm-popover
                onMouseDown={(e) => e.stopPropagation()}
                style={{
                  position: "absolute",
                  top: "100%",
                  right: 10,
                  marginTop: 2,
                  backgroundColor: colors.surface,
                  border: `1px solid ${colors.textLight}`,
                  borderRadius: 6,
                  padding: "0 8px",
                  height: 22,
                  boxSizing: "border-box",
                  zIndex: 1000,
                  boxShadow: `0 4px 12px ${colors.shadow}`,
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  whiteSpace: "nowrap",
                  fontFamily: fonts.sans,
                  fontSize: 9,
                }}
              >
                <svg width="16" height="9" viewBox="0 0 16 9" style={{ position: "absolute", top: -8, right: 8, filter: "drop-shadow(0 -2px 4px rgba(0,0,0,0.3))" }}>
                  <path d="M1 9 L7 2.5 Q8 1.5 9 2.5 L15 9 Z" fill={colors.surface} stroke={colors.textLight} strokeWidth="0.75" />
                  <rect x="0" y="8" width="16" height="2" fill={colors.surface} />
                </svg>
                <span style={{ color: colors.textLight }}>Delete?</span>
                <button
                  onClick={() => {
                    if (confirmingId) { setConfirmingId(null); handleDelete(wt); }
                    else { setConfirmingPath(null); handleRemove(wt); }
                  }}
                  style={{
                    background: colors.dangerBg,
                    border: `1px solid ${colors.dangerText}`,
                    color: colors.dangerText,
                    cursor: "pointer",
                    padding: "1px 6px",
                    fontSize: 9,
                    fontFamily: fonts.sans,
                    borderRadius: 4,
                    lineHeight: 1.4,
                  }}
                  onMouseEnter={(e) => { e.currentTarget.style.background = colors.dangerHoverBg; e.currentTarget.style.color = colors.white; }}
                  onMouseLeave={(e) => { e.currentTarget.style.background = colors.dangerBg; e.currentTarget.style.color = colors.dangerText; }}
                >
                  Yes
                </button>
                <button
                  onClick={() => { setConfirmingId(null); setConfirmingPath(null); }}
                  style={{
                    background: "none",
                    border: `1px solid ${colors.border}`,
                    color: colors.textDim,
                    cursor: "pointer",
                    padding: "1px 6px",
                    fontSize: 9,
                    fontFamily: fonts.sans,
                    borderRadius: 4,
                    lineHeight: 1.4,
                  }}
                  onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; e.currentTarget.style.borderColor = colors.textDim; }}
                  onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; e.currentTarget.style.borderColor = colors.border; }}
                >
                  No
                </button>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
