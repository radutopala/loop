import { useState, useCallback, useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import { fetchBranches, type BranchInfo } from "../../api/git";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { logErr } from "../../utils/log";

/**
 * A "+wt" button for a sidebar channel row. Clicking it opens a branch
 * dropdown (the same data the header branch picker uses); picking a branch
 * creates a worktree thread from it. Only rendered for channels backed by a
 * git repo (a dir_path).
 *
 * The dropdown is portaled to the document body and positioned at the button's
 * lower-right via fixed coordinates, so it opens down-and-to-the-right of the
 * click instead of being clipped by the sidebar's overflow.
 */
export function SidebarWorktreeButton({ channelId, onCreateWorktree }: {
  channelId: string;
  onCreateWorktree: (channelId: string, branch: string) => void;
}) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const [coords, setCoords] = useState<{ top: number; left: number }>({ top: 0, left: 0 });
  const [branchInfo, setBranchInfo] = useState<BranchInfo | null>(null);
  const [search, setSearch] = useState("");
  const buttonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (buttonRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const handleOpen = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    if (open) {
      setOpen(false);
      return;
    }
    const rect = buttonRef.current?.getBoundingClientRect();
    if (rect) setCoords({ top: rect.bottom + 4, left: rect.left });
    setSearch("");
    setBranchInfo(null);
    setOpen(true);
    fetchBranches(channelId).then(setBranchInfo).catch(logErr("fetching branches for worktree"));
    setTimeout(() => searchRef.current?.focus(), 0);
  }, [channelId, open]);

  const filtered = branchInfo?.branches.filter((b) =>
    !search || b.toLowerCase().includes(search.toLowerCase()),
  ) ?? [];

  return (
    <>
      <button
        ref={buttonRef}
        onClick={handleOpen}
        title="New worktree from branch"
        style={{
          background: "none",
          border: "none",
          color: colors.textDim,
          cursor: "pointer",
          padding: "2px 6px",
          fontSize: 11,
          lineHeight: 1,
          borderRadius: 4,
          whiteSpace: "nowrap",
        }}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
      >
        +wt
      </button>
      {open && createPortal(
        <div
          ref={menuRef}
          data-testid="sidebar-worktree-picker"
          onClick={(e) => e.stopPropagation()}
          style={{
            position: "fixed",
            top: coords.top,
            left: coords.left,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            zIndex: 1000,
            minWidth: 260,
            maxHeight: "min(360px, 60vh)",
            display: "flex",
            flexDirection: "column",
            boxShadow: `0 4px 12px ${colors.shadow}`,
          }}
        >
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
                placeholder="Worktree from branch…"
                style={{ flex: 1, background: "none", border: "none", outline: "none", color: colors.textLight, fontSize: 12, fontFamily: fonts.sans }}
              />
            </div>
          </div>
          <div style={{ flex: 1, minHeight: 0, overflow: "auto", padding: "4px 0" }}>
            {filtered.map((b) => (
              <button
                key={b}
                onClick={() => { setOpen(false); onCreateWorktree(channelId, b); }}
                title={`New worktree thread from ${b}`}
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
                  whiteSpace: "nowrap",
                }}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
                  <line x1="6" y1="3" x2="6" y2="15" />
                  <circle cx="18" cy="6" r="3" />
                  <circle cx="6" cy="18" r="3" />
                  <path d="M18 9a9 9 0 0 1-9 9" />
                </svg>
                <span style={{ flex: 1 }}>{b}</span>
              </button>
            ))}
            {branchInfo && filtered.length === 0 && (
              <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No branches found</div>
            )}
            {!branchInfo && (
              <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>Loading branches…</div>
            )}
          </div>
        </div>,
        document.body,
      )}
    </>
  );
}
