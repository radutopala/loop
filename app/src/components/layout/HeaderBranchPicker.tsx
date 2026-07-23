import { useCallback, useEffect, useRef, useState } from "react";
import { type BranchInfo, fetchBranches, switchBranch } from "../../api/loopApi";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { logErr } from "../../utils/log";

export function HeaderBranchPicker({
  channelId,
  branch,
  onBranchChanged,
  onCreateWorktree,
  onImportWorktree,
  onSelectThread,
  onError,
}: {
  channelId: string;
  branch: string;
  onBranchChanged?: () => void;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
  onImportWorktree?: (channelId: string, worktreePath: string) => Promise<void>;
  onSelectThread?: (threadId: string) => void;
  onError?: (msg: string) => void;
}) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const [branchInfo, setBranchInfo] = useState<BranchInfo | null>(null);
  const [search, setSearch] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const handleOpen = useCallback(() => {
    setOpen(true);
    setSearch("");
    fetchBranches(channelId).then(setBranchInfo).catch(logErr("fetching branches"));
    setTimeout(() => searchRef.current?.focus(), 0);
  }, [channelId]);

  const handleSelect = useCallback(
    async (b: string) => {
      setOpen(false);
      if (b === branch) return;
      try {
        await switchBranch(channelId, b);
        onBranchChanged?.();
      } catch (e) {
        onError?.(e instanceof Error ? e.message : "Failed to switch branch");
      }
    },
    [channelId, branch, onBranchChanged, onError],
  );

  const filtered = branchInfo?.branches.filter((b) => !search || b.toLowerCase().includes(search.toLowerCase())) ?? [];

  const lowerSearch = search.toLowerCase();
  const filteredWorktrees = (branchInfo?.worktrees ?? []).filter((wt) => !search || wt.branch.toLowerCase().includes(lowerSearch) || wt.path.split("/").pop()?.toLowerCase().includes(lowerSearch));

  const hasWorktrees = (branchInfo?.worktrees ?? []).length > 0 && (!!onImportWorktree || !!onSelectThread);

  return (
    <div ref={ref} style={{ position: "relative", display: "flex", alignItems: "center" }}>
      <button
        onClick={handleOpen}
        title="Branch"
        style={{
          display: "flex",
          alignItems: "center",
          gap: 2,
          background: "none",
          border: "none",
          cursor: "pointer",
          padding: 0,
          fontSize: 11,
          color: colors.active,
          fontFamily: fonts.mono,
          flexShrink: 0,
          WebkitAppRegion: "no-drag",
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.opacity = "0.8";
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.opacity = "1";
        }}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2 }}>
          <line x1="6" y1="3" x2="6" y2="15" />
          <circle cx="18" cy="6" r="3" />
          <circle cx="6" cy="18" r="3" />
          <path d="M18 9a9 9 0 0 1-9 9" />
        </svg>
        {branch}
        <svg width="8" height="8" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="2" style={{ opacity: 0.5, marginLeft: 1 }}>
          <polyline points="2,3 5,7 8,3" />
        </svg>
      </button>
      {open && (
        <div
          data-testid="branch-picker"
          style={{
            position: "absolute",
            top: "100%",
            left: 0,
            marginTop: 4,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            padding: 0,
            zIndex: 1000,
            minWidth: 340,
            maxHeight: hasWorktrees ? undefined : "min(400px, 70vh)",
            height: hasWorktrees ? "min(400px, 70vh)" : undefined,
            display: "flex",
            flexDirection: "column",
            boxShadow: `0 4px 12px ${colors.shadow}`,
          }}
        >
          {/* Search */}
          <div style={{ padding: "8px 8px 4px", flexShrink: 0 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 8px", backgroundColor: colors.bg, border: `1px solid ${colors.border}`, borderRadius: 6 }}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke={colors.textDim} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="11" cy="11" r="8" />
                <line x1="21" y1="21" x2="16.65" y2="16.65" />
              </svg>
              <input
                ref={searchRef}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={hasWorktrees ? "Search branches & worktrees" : "Search branches"}
                style={{
                  flex: 1,
                  background: "none",
                  border: "none",
                  outline: "none",
                  color: colors.textLight,
                  fontSize: 12,
                  fontFamily: fonts.sans,
                }}
              />
            </div>
          </div>
          {/* Branches section */}
          <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "4px 0" }}>
            <div style={{ padding: "4px 12px 2px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Branches</div>
            {filtered.map((b) => {
              const isCurrent = b === (branchInfo?.current ?? branch);
              return (
                <div
                  key={b}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    borderRadius: 4,
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)";
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.backgroundColor = "transparent";
                  }}
                >
                  <button
                    onClick={() => handleSelect(b)}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 6,
                      flex: 1,
                      padding: "5px 0 5px 12px",
                      border: "none",
                      background: "transparent",
                      color: isCurrent ? colors.textLight : colors.text,
                      cursor: "pointer",
                      fontSize: 12,
                      fontFamily: fonts.mono,
                      textAlign: "left",
                      whiteSpace: "nowrap",
                    }}
                  >
                    <svg
                      width="12"
                      height="12"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      style={{ flexShrink: 0, opacity: 0.5 }}
                    >
                      <line x1="6" y1="3" x2="6" y2="15" />
                      <circle cx="18" cy="6" r="3" />
                      <circle cx="6" cy="18" r="3" />
                      <path d="M18 9a9 9 0 0 1-9 9" />
                    </svg>
                    <span style={{ flex: 1 }}>{b}</span>
                    {isCurrent && <span style={{ color: colors.active, flexShrink: 0 }}>&#10003;</span>}
                  </button>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      navigator.clipboard.writeText(b);
                    }}
                    title="Copy branch name"
                    style={{
                      padding: "2px 6px",
                      border: "none",
                      background: "transparent",
                      color: colors.textDim,
                      cursor: "pointer",
                      borderRadius: 4,
                      flexShrink: 0,
                      fontSize: 10,
                      fontFamily: fonts.mono,
                    }}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.color = colors.textLight;
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.color = colors.textDim;
                    }}
                  >
                    cp
                  </button>
                  {onCreateWorktree && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        setOpen(false);
                        onCreateWorktree(channelId, b);
                      }}
                      title={`New worktree thread from ${b}`}
                      style={{
                        padding: "2px 6px",
                        border: "none",
                        background: "transparent",
                        color: colors.textDim,
                        cursor: "pointer",
                        borderRadius: 4,
                        flexShrink: 0,
                        fontSize: 10,
                        fontFamily: fonts.mono,
                      }}
                      onMouseEnter={(e) => {
                        e.currentTarget.style.color = colors.active;
                      }}
                      onMouseLeave={(e) => {
                        e.currentTarget.style.color = colors.textDim;
                      }}
                    >
                      +wt
                    </button>
                  )}
                </div>
              );
            })}
            {filtered.length === 0 && <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No branches found</div>}
          </div>
          {/* Worktrees section */}
          {hasWorktrees && (
            <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "4px 0", borderTop: `1px solid ${colors.border}` }}>
              <div style={{ padding: "4px 12px 2px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Worktrees</div>
              {filteredWorktrees.map((wt) => {
                const dirName = wt.path.split("/").pop() || wt.path;
                const hasThread = !!wt.thread_id;
                return (
                  <div
                    key={wt.path}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      borderRadius: 4,
                    }}
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
                      <svg
                        width="12"
                        height="12"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke={colors.textDim}
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        style={{ flexShrink: 0, opacity: 0.5 }}
                      >
                        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                      </svg>
                      <span style={{ color: colors.text, overflow: "hidden", textOverflow: "ellipsis" }}>{wt.branch || "(detached)"}</span>
                      <span style={{ color: colors.textDim, fontSize: 10, flexShrink: 0 }}>{dirName}</span>
                    </div>
                    {hasThread && onSelectThread ? (
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setOpen(false);
                          onSelectThread(wt.thread_id!);
                        }}
                        title="Go to thread"
                        style={{
                          padding: "2px 6px",
                          border: "none",
                          background: "transparent",
                          color: colors.active,
                          cursor: "pointer",
                          borderRadius: 4,
                          flexShrink: 0,
                          fontSize: 10,
                          fontFamily: fonts.mono,
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.opacity = "0.7";
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.opacity = "1";
                        }}
                      >
                        go
                      </button>
                    ) : onImportWorktree ? (
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          setOpen(false);
                          onImportWorktree(channelId, wt.path);
                        }}
                        title="Import worktree as thread"
                        style={{
                          padding: "2px 6px",
                          border: "none",
                          background: "transparent",
                          color: colors.textDim,
                          cursor: "pointer",
                          borderRadius: 4,
                          flexShrink: 0,
                          fontSize: 10,
                          fontFamily: fonts.mono,
                        }}
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
              {filteredWorktrees.length === 0 && <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No worktrees found</div>}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
