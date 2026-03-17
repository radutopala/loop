import { useCallback, useEffect, useRef, useState } from "react";
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";
import { fetchBranches, type BranchInfo } from "../api/loopApi";

interface ChatToolbarProps {
  channelId: string;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
}

export function ChatToolbar({ channelId, onCreateWorktree }: ChatToolbarProps) {
  if (!onCreateWorktree) return null;

  return (
    <div
      style={{
        display: "flex",
        justifyContent: "center",
        padding: "0 24px 4px",
      }}
    >
    <div
      style={{
        display: "flex",
        alignItems: "center",
        width: "100%",
        maxWidth: 768,
        gap: 8,
        fontSize: 11,
        fontFamily: fonts.mono,
      }}
    >
      <div style={{ flex: 1 }} />
      <NewWorktreeButton channelId={channelId} onCreateWorktree={onCreateWorktree} />
    </div>
    </div>
  );
}

// ── New Worktree Button ──

function NewWorktreeButton({ channelId, onCreateWorktree }: {
  channelId: string;
  onCreateWorktree: (channelId: string, branch: string) => Promise<void>;
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
    fetchBranches(channelId).then(setBranchInfo).catch(() => {});
    setTimeout(() => searchRef.current?.focus(), 0);
  }, [channelId]);

  const handleSelect = useCallback((branch: string) => {
    setOpen(false);
    onCreateWorktree(channelId, branch);
  }, [channelId, onCreateWorktree]);

  const filtered = branchInfo?.branches.filter((b) =>
    !search || b.toLowerCase().includes(search.toLowerCase()),
  ) ?? [];

  return (
    <div ref={ref} style={{ position: "relative" }}>
      <button
        onClick={handleOpen}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 4,
          padding: "3px 8px",
          border: `1px solid ${colors.border}`,
          borderRadius: 12,
          background: "transparent",
          color: colors.textDim,
          cursor: "pointer",
          fontSize: 11,
          fontFamily: fonts.mono,
        }}
        onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; e.currentTarget.style.color = colors.textLight; }}
        onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.color = colors.textDim; }}
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="6" y1="3" x2="6" y2="15" />
          <circle cx="18" cy="6" r="3" />
          <circle cx="6" cy="18" r="3" />
          <path d="M18 9a9 9 0 0 1-9 9" />
        </svg>
        New worktree thread
        <svg width="8" height="8" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="2" style={{ opacity: 0.5 }}>
          <polyline points="2,3 5,7 8,3" />
        </svg>
      </button>
      {open && (
        <div
          style={{
            position: "absolute",
            bottom: "100%",
            right: 0,
            marginBottom: 4,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            padding: 0,
            zIndex: 1000,
            minWidth: 340,
            height: 400,
            maxHeight: "60vh",
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
                placeholder="Create worktree from branch..."
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
          {/* Branch list */}
          <div style={{ overflow: "auto", padding: "4px 0", flex: "1 1 0", minHeight: 0 }}>
            <div style={{ padding: "4px 12px 2px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
              Base branch
            </div>
            {filtered.map((branch) => (
              <button
                key={branch}
                onClick={() => handleSelect(branch)}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  width: "100%",
                  padding: "5px 12px",
                  border: "none",
                  background: "transparent",
                  color: colors.text,
                  cursor: "pointer",
                  fontSize: 12,
                  fontFamily: fonts.mono,
                  textAlign: "left",
                  borderRadius: 4,
                  whiteSpace: "nowrap",
                }}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.text; }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
                  <line x1="6" y1="3" x2="6" y2="15" />
                  <circle cx="18" cy="6" r="3" />
                  <circle cx="6" cy="18" r="3" />
                  <path d="M18 9a9 9 0 0 1-9 9" />
                </svg>
                <span style={{ flex: 1 }}>{branch}</span>
              </button>
            ))}
            {filtered.length === 0 && (
              <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No branches found</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
