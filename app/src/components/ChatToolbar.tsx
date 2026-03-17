import { useCallback, useEffect, useRef, useState } from "react";
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";
import type { ChatState } from "../hooks/useChatState";
import { fetchBranches, createBranch, type BranchInfo } from "../api/loopApi";

interface ChatToolbarProps {
  channelId: string;
  chatState: ChatState;
  currentBranch: string;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
}

export function ChatToolbar({ channelId, chatState, currentBranch, onCreateWorktree }: ChatToolbarProps) {
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
      <EnvironmentSelector env={chatState.env} setEnv={chatState.setEnv} />
      <div style={{ flex: 1 }} />
      <BranchPicker
        channelId={channelId}
        env={chatState.env}
        currentBranch={currentBranch}
        selectedBranch={chatState.selectedBranch}
        setSelectedBranch={chatState.setSelectedBranch}
        setWorktreePath={chatState.setWorktreePath}
        setEnv={chatState.setEnv}
        onCreateWorktree={onCreateWorktree}
      />
    </div>
    </div>
  );
}

// ── Environment Selector ──

function EnvironmentSelector({ env, setEnv }: { env: "local" | "worktree"; setEnv: (e: "local" | "worktree") => void }) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const label = env === "local" ? "Local" : "Worktree";

  return (
    <div ref={ref} style={{ position: "relative" }}>
      <button
        onClick={() => setOpen((v) => !v)}
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
        {env === "local" ? (
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
            <line x1="8" y1="21" x2="16" y2="21" />
            <line x1="12" y1="17" x2="12" y2="21" />
          </svg>
        ) : (
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="6" y1="3" x2="6" y2="15" />
            <circle cx="18" cy="6" r="3" />
            <circle cx="6" cy="18" r="3" />
            <path d="M18 9a9 9 0 0 1-9 9" />
          </svg>
        )}
        {label}
        <svg width="8" height="8" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="2" style={{ opacity: 0.5 }}>
          <polyline points="2,3 5,7 8,3" />
        </svg>
      </button>
      {open && (
        <div
          style={{
            position: "absolute",
            bottom: "100%",
            left: 0,
            marginBottom: 4,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.border}`,
            borderRadius: 8,
            padding: 4,
            zIndex: 1000,
            minWidth: 160,
            boxShadow: `0 4px 12px ${colors.shadow}`,
          }}
        >
          <div style={{ padding: "6px 8px 4px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
            Continue in
          </div>
          {(["local", "worktree"] as const).map((item) => (
            <button
              key={item}
              onClick={() => { setEnv(item); setOpen(false); }}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                width: "100%",
                padding: "6px 8px",
                border: "none",
                background: "transparent",
                color: colors.textLight,
                cursor: "pointer",
                fontSize: 12,
                fontFamily: fonts.sans,
                borderRadius: 4,
                textAlign: "left",
              }}
              onMouseEnter={(ev) => { ev.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; }}
              onMouseLeave={(ev) => { ev.currentTarget.style.backgroundColor = "transparent"; }}
            >
              {item === "local" ? (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
                  <line x1="8" y1="21" x2="16" y2="21" />
                  <line x1="12" y1="17" x2="12" y2="21" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="6" y1="3" x2="6" y2="15" />
                  <circle cx="18" cy="6" r="3" />
                  <circle cx="6" cy="18" r="3" />
                  <path d="M18 9a9 9 0 0 1-9 9" />
                </svg>
              )}
              <span style={{ flex: 1 }}>{item === "local" ? "Local" : "New worktree"}</span>
              {env === item && <span style={{ color: colors.active }}>&#10003;</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// ── Branch Picker ──

function BranchPicker({ channelId, env, currentBranch, selectedBranch, setSelectedBranch, setWorktreePath, setEnv, onCreateWorktree }: {
  channelId: string;
  env: "local" | "worktree";
  currentBranch: string;
  selectedBranch: string | null;
  setSelectedBranch: (branch: string | null) => void;
  setWorktreePath: (path: string | null) => void;
  setEnv: (env: "local" | "worktree") => void;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
}) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const [branchInfo, setBranchInfo] = useState<BranchInfo | null>(null);
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  const displayBranch = selectedBranch || currentBranch || "main";

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) { setOpen(false); setCreating(false); }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const handleOpen = useCallback(() => {
    setOpen(true);
    setSearch("");
    setCreating(false);
    fetchBranches(channelId).then(setBranchInfo).catch(() => {});
    setTimeout(() => searchRef.current?.focus(), 0);
  }, [channelId]);

  const handleSelect = useCallback((branch: string) => {
    if (env === "worktree" && onCreateWorktree) {
      setOpen(false);
      onCreateWorktree(channelId, branch);
      return;
    }
    setSelectedBranch(branch);
    setOpen(false);
  }, [env, channelId, onCreateWorktree, setSelectedBranch]);

  const handleCreate = useCallback(async () => {
    const trimmed = newName.trim();
    if (!trimmed) return;
    try {
      await createBranch(channelId, trimmed, displayBranch);
      setSelectedBranch(trimmed);
    } catch { /* ignore */ }
    setCreating(false);
    setNewName("");
    setOpen(false);
  }, [channelId, newName, displayBranch, setSelectedBranch]);

  const filtered = branchInfo?.branches.filter((b) =>
    !search || b.toLowerCase().includes(search.toLowerCase()),
  ) ?? [];

  const filteredWorktrees = branchInfo?.worktrees.filter((wt) =>
    !search || wt.branch.toLowerCase().includes(search.toLowerCase()) || wt.path.toLowerCase().includes(search.toLowerCase()),
  ) ?? [];

  const label = env === "worktree" ? `From ${displayBranch}` : displayBranch;

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
        {label}
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
                placeholder="Search branches"
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
              Branches
            </div>
            {filtered.map((branch) => {
              const isCurrent = branch === (branchInfo?.current ?? currentBranch);
              const isSelected = branch === displayBranch;
              return (
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
                    color: isCurrent ? colors.textLight : colors.text,
                    cursor: "pointer",
                    fontSize: 12,
                    fontFamily: fonts.mono,
                    textAlign: "left",
                    borderRadius: 4,
                    whiteSpace: "nowrap",
                  }}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; e.currentTarget.style.color = colors.textLight; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = isCurrent ? colors.textLight : colors.text; }}
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.5 }}>
                    <line x1="6" y1="3" x2="6" y2="15" />
                    <circle cx="18" cy="6" r="3" />
                    <circle cx="6" cy="18" r="3" />
                    <path d="M18 9a9 9 0 0 1-9 9" />
                  </svg>
                  <span style={{ flex: 1 }}>{branch}</span>
                  {isSelected && <span style={{ color: colors.active, flexShrink: 0 }}>&#10003;</span>}
                </button>
              );
            })}
            {filtered.length === 0 && (
              <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 12 }}>No branches found</div>
            )}
          </div>
          {/* Worktrees */}
          {filteredWorktrees.length > 0 && env === "local" && (
            <div style={{ borderTop: `1px solid ${colors.border}`, padding: "4px 0", overflow: "auto", flex: "1 1 0", minHeight: 0 }}>
              <div style={{ padding: "4px 12px 2px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
                Worktrees
              </div>
              {filteredWorktrees.map((wt) => (
                <button
                  key={wt.path}
                  onClick={() => { setWorktreePath(wt.path); setSelectedBranch(wt.branch); setEnv("worktree"); setOpen(false); }}
                  title={wt.path}
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
                  <span style={{ flex: 1 }}>{wt.branch}</span>
                  <span style={{ fontSize: 10, color: colors.textDim }}>{wt.path.split("/").pop()}</span>
                </button>
              ))}
            </div>
          )}
          {/* Create new branch */}
          <div style={{ borderTop: `1px solid ${colors.border}`, padding: 4 }}>
            {creating ? (
              <div style={{ padding: "4px 8px" }}>
                <input
                  autoFocus
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") handleCreate(); if (e.key === "Escape") setCreating(false); }}
                  placeholder="branch-name"
                  style={{
                    width: "100%",
                    boxSizing: "border-box",
                    padding: "4px 8px",
                    fontSize: 12,
                    fontFamily: fonts.mono,
                    backgroundColor: colors.bg,
                    border: `1px solid ${colors.active}`,
                    borderRadius: 4,
                    color: colors.textLight,
                    outline: "none",
                  }}
                />
              </div>
            ) : (
              <button
                onClick={() => setCreating(true)}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  width: "100%",
                  padding: "6px 8px",
                  border: "none",
                  background: "transparent",
                  color: colors.textDim,
                  cursor: "pointer",
                  fontSize: 12,
                  fontFamily: fonts.sans,
                  borderRadius: 4,
                  textAlign: "left",
                }}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)"; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="12" y1="5" x2="12" y2="19" />
                  <line x1="5" y1="12" x2="19" y2="12" />
                </svg>
                Create and checkout new branch...
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
