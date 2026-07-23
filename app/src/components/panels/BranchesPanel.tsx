import { useCallback, useEffect, useState } from "react";
import { createWorktreeThread } from "../../api/channels";
import { deleteBranch, fetchBranches, switchBranch } from "../../api/git";
import { useEventStream } from "../../hooks/useEventStream";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

interface BranchesPanelProps {
  channelId: string;
  isWorktree: boolean;
  hasBranch: boolean;
  onSelectThread?: (threadId: string) => void;
  onBranchChanged?: () => void;
}

export function BranchesPanel({ channelId, isWorktree, hasBranch, onSelectThread, onBranchChanged }: BranchesPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [branches, setBranches] = useState<string[]>([]);
  const [current, setCurrent] = useState("");
  const [loading, setLoading] = useState(true);
  const [switchingBranch, setSwitchingBranch] = useState<string | null>(null);
  const [deletingBranch, setDeletingBranch] = useState<string | null>(null);
  const [creatingWt, setCreatingWt] = useState<string | null>(null);
  const [confirmingBranch, setConfirmingBranch] = useState<string | null>(null);

  useEffect(() => {
    if (!confirmingBranch) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest("[data-confirm-popover]")) {
        setConfirmingBranch(null);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirmingBranch]);

  const loadBranches = useCallback(async () => {
    try {
      const info = await fetchBranches(channelId);
      setBranches(info.branches ?? []);
      setCurrent(info.current ?? "");
    } catch {
      setBranches([]);
    } finally {
      setLoading(false);
    }
  }, [channelId]);

  useEffect(() => {
    loadBranches();
  }, [loadBranches]);

  const onEvent = useCallback(
    (event: { type: string }) => {
      if (event.type === "channel.created" || event.type === "channel.deleted") {
        loadBranches();
      }
    },
    [loadBranches],
  );

  useEventStream({ channelId, onEvent });

  const handleSwitch = async (branch: string) => {
    setSwitchingBranch(branch);
    try {
      await switchBranch(channelId, branch);
      await loadBranches();
      onBranchChanged?.();
    } catch {
      // ignore
    } finally {
      setSwitchingBranch(null);
    }
  };

  const handleDelete = async (branch: string) => {
    setDeletingBranch(branch);
    try {
      await deleteBranch(channelId, branch);
      await loadBranches();
    } catch {
      // ignore
    } finally {
      setDeletingBranch(null);
    }
  };

  const handleCreateWorktree = async (branch: string) => {
    setCreatingWt(branch);
    try {
      const result = await createWorktreeThread(channelId, branch);
      if (result.threadId && onSelectThread) {
        onSelectThread(result.threadId);
      }
      await loadBranches();
    } catch {
      // ignore
    } finally {
      setCreatingWt(null);
    }
  };

  if (isWorktree) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>Branches are managed from the parent channel</div>
    );
  }

  if (!hasBranch) {
    return <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>No git repository detected</div>;
  }

  if (loading) {
    return <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>Loading...</div>;
  }

  if (branches.length === 0) {
    return <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>No branches</div>;
  }

  const btnStyle: React.CSSProperties = {
    padding: "2px 8px",
    minWidth: 48,
    fontSize: 10,
    lineHeight: 1.4,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    background: "transparent",
    color: colors.textLight,
    cursor: "pointer",
    textAlign: "center",
  };

  return (
    <div data-testid="branches-panel" style={{ flex: 1, overflow: "auto", padding: 12 }}>
      {branches.map((branch) => {
        const isCurrent = branch === current;
        const isSwitching = switchingBranch === branch;
        const isDeleting = deletingBranch === branch;
        const isCreating = creatingWt === branch;

        return (
          <div
            key={branch}
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
              <div style={{ fontSize: fontSizes.panels, color: colors.textLight, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", fontWeight: isCurrent ? 600 : 400 }}>
                {branch}
                {isCurrent ? " *" : ""}
              </div>
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 6, marginLeft: 12, flexShrink: 0 }}>
              {!isCurrent && (
                <button
                  style={btnStyle}
                  onClick={() => handleSwitch(branch)}
                  disabled={isSwitching}
                  title="Switch to this branch"
                  onMouseEnter={(e) => {
                    e.currentTarget.style.borderColor = colors.textLight;
                    e.currentTarget.style.color = colors.white;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.borderColor = colors.border;
                    e.currentTarget.style.color = colors.textLight;
                  }}
                >
                  {isSwitching ? "..." : "Switch"}
                </button>
              )}
              <button
                style={btnStyle}
                onClick={() => handleCreateWorktree(branch)}
                disabled={isCreating}
                title="Create worktree from this branch"
                onMouseEnter={(e) => {
                  e.currentTarget.style.borderColor = colors.textLight;
                  e.currentTarget.style.color = colors.white;
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.borderColor = colors.border;
                  e.currentTarget.style.color = colors.textLight;
                }}
              >
                {isCreating ? "..." : "+wt"}
              </button>
              {!isCurrent && (
                <button
                  style={{ ...btnStyle, color: isDeleting ? colors.textDim : "#ef4444" }}
                  onClick={() => setConfirmingBranch(branch)}
                  disabled={isDeleting}
                  title="Delete this branch"
                  onMouseEnter={(e) => {
                    e.currentTarget.style.borderColor = colors.textLight;
                    e.currentTarget.style.color = colors.dangerText;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.borderColor = colors.border;
                    e.currentTarget.style.color = "#ef4444";
                  }}
                >
                  {isDeleting ? "..." : "Delete"}
                </button>
              )}
            </div>
            {confirmingBranch === branch && (
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
                    setConfirmingBranch(null);
                    handleDelete(branch);
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
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = colors.dangerHoverBg;
                    e.currentTarget.style.color = colors.white;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = colors.dangerBg;
                    e.currentTarget.style.color = colors.dangerText;
                  }}
                >
                  Yes
                </button>
                <button
                  onClick={() => setConfirmingBranch(null)}
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
                  onMouseEnter={(e) => {
                    e.currentTarget.style.color = colors.textLight;
                    e.currentTarget.style.borderColor = colors.textDim;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.color = colors.textDim;
                    e.currentTarget.style.borderColor = colors.border;
                  }}
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
