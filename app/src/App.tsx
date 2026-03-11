import { useCallback, useEffect, useRef, useState } from "react";
import type { Channel, ViewMode, WSEvent } from "./types";
import { colors, fonts } from "./theme";
import { createChannel, createThread, deleteChannel, deleteThread, fetchChannels, fetchDiff, initApiUrl } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { TerminalPanes, SplitMenu } from "./components/TerminalPanes";
import type { TerminalPanesRef } from "./components/TerminalPanes";
import { ChatView } from "./components/ChatView";
import { ModeToggle } from "./components/ModeToggle";
import { DiffPanel } from "./components/DiffPanel";
import { ShellPanel } from "./components/ShellPanel";
import { CommandPalette } from "./components/CommandPalette";
import { Settings } from "./components/Settings";
import { useEventStream } from "./hooks/useEventStream";

const MODE_STORAGE_KEY = "loop-view-mode";
const SHELL_OPEN_KEY = "loop-shell-open";

function loadMode(channelId: string | null): ViewMode {
  if (!channelId) return "chat";
  try {
    const stored = localStorage.getItem(MODE_STORAGE_KEY);
    if (stored) {
      const prefs: Record<string, ViewMode> = JSON.parse(stored);
      if (prefs[channelId] === "chat" || prefs[channelId] === "terminal") {
        return prefs[channelId];
      }
    }
  } catch {
    /* ignore corrupt storage */
  }
  return "chat";
}

function saveMode(channelId: string, mode: ViewMode) {
  try {
    const stored = localStorage.getItem(MODE_STORAGE_KEY);
    const prefs: Record<string, ViewMode> = stored ? JSON.parse(stored) : {};
    prefs[channelId] = mode;
    localStorage.setItem(MODE_STORAGE_KEY, JSON.stringify(prefs));
  } catch {
    /* ignore storage errors */
  }
}

function loadShellOpen(channelId: string | null): boolean {
  if (!channelId) return false;
  try {
    const stored = localStorage.getItem(SHELL_OPEN_KEY);
    if (stored) {
      const prefs = JSON.parse(stored);
      if (typeof prefs === "object" && prefs !== null) {
        return prefs[channelId] === true;
      }
    }
  } catch { /* ignore */ }
  return false;
}

function saveShellOpen(channelId: string, open: boolean) {
  try {
    const stored = localStorage.getItem(SHELL_OPEN_KEY);
    const parsed = stored ? JSON.parse(stored) : null;
    const prefs: Record<string, boolean> = (typeof parsed === "object" && parsed !== null) ? parsed : {};
    prefs[channelId] = open;
    localStorage.setItem(SHELL_OPEN_KEY, JSON.stringify(prefs));
  } catch { /* ignore */ }
}

function getHashChannelId(): string | null {
  const hash = window.location.hash.slice(1);
  return hash || null;
}

export default function App() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(getHashChannelId);
  const [ready, setReady] = useState(false);
  const [viewMode, setViewMode] = useState<ViewMode>("chat");
  const [error, setError] = useState<string | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [mountKey, setMountKey] = useState(0);
  const [diffOpen, setDiffOpen] = useState(false);
  const [diffMaximized, setDiffMaximized] = useState(false);
  const [diffStats, setDiffStats] = useState<{ add: number; del: number }>({ add: 0, del: 0 });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsDirPath, setSettingsDirPath] = useState<string | null>(null);
  const [scrollToMessageId, setScrollToMessageId] = useState<number | null>(null);
  const [shellOpen, setShellOpen] = useState(() => loadShellOpen(getHashChannelId()));
  const [shellMaximized, setShellMaximized] = useState(false);
  const [agentSplitMenu, setAgentSplitMenu] = useState(false);
  const [agentKillSignal, setAgentKillSignal] = useState(0);
  const [agentRunning, setAgentRunning] = useState(true);
  const agentPanesRef = useRef<TerminalPanesRef>(null);

  // Fetch diff stats for the selected channel and keep them updated via events.
  const loadDiffStats = useCallback(async () => {
    if (!selectedId) return;
    try {
      const d = await fetchDiff(selectedId);
      setDiffStats({ add: d.total_additions, del: d.total_deletions });
    } catch {
      /* ignore */
    }
  }, [selectedId]);

  useEffect(() => {
    setDiffStats({ add: 0, del: 0 });
    loadDiffStats();
  }, [loadDiffStats]);

  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    initApiUrl().then(() => setReady(true));
  }, []);

  // Sync hash with selected channel.
  useEffect(() => {
    window.location.hash = selectedId ? selectedId : "";
  }, [selectedId]);

  // Handle back/forward navigation.
  useEffect(() => {
    const onHashChange = () => {
      const id = getHashChannelId();
      setSelectedId(id);
      setViewMode(loadMode(id));
      setShellOpen(loadShellOpen(id));
      setShellMaximized(false);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  // Handle deep link from protocol URL (loop://channel/<id>).
  useEffect(() => {
    if (window.loopAPI?.onNavigateChannel) {
      window.loopAPI.onNavigateChannel((channelId: string) => {
        setSelectedId(channelId);
        setViewMode(loadMode(channelId));
        setShellOpen(loadShellOpen(channelId));
        setShellMaximized(false);
      });
    }
  }, []);

  // Cmd+K / Ctrl+K to toggle command palette.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
      if ((e.metaKey || e.ctrlKey) && e.key === ",") {
        e.preventDefault();
        setSettingsOpen((v) => {
          if (!v) { setDiffOpen(false); setDiffMaximized(false); setShellOpen(false); setShellMaximized(false); }
          return !v;
        });
        setSettingsDirPath(null);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // Listen for Settings menu item from main process.
  useEffect(() => {
    if (window.loopAPI?.onOpenSettings) {
      window.loopAPI.onOpenSettings(() => { setSettingsOpen(true); setSettingsDirPath(null); setDiffOpen(false); setDiffMaximized(false); setShellOpen(false); setShellMaximized(false); });
    }
  }, []);

  const dmEnsuredRef = useRef(false);

  const loadChannels = useCallback(async () => {
    if (!ready) return;
    try {
      let chs = await fetchChannels();
      // Ensure a DM channel exists (once per session).
      if (!dmEnsuredRef.current) {
        dmEnsuredRef.current = true;
        const hasDm = chs.some((c) => c.name === "dm" && !c.parent_id);
        if (!hasDm) {
          try {
            await createChannel("dm");
            chs = await fetchChannels();
          } catch { /* ignore — channel creation might not be configured */ }
        }
      }
      setChannels(chs);
    } catch {
      /* will retry on next poll */
    }
  }, [ready]);

  useEffect(() => {
    loadChannels();
    const id = setInterval(loadChannels, 30_000);
    return () => clearInterval(id);
  }, [loadChannels]);

  const onAppEvent = useCallback((event: WSEvent) => {
    if (event.type === "channel.created") {
      loadChannels();
      return;
    }
    if (event.type === "channel.deleted") {
      if (event.channel_id === selectedId) {
        setSelectedId(null);
      }
      loadChannels();
      return;
    }
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(loadDiffStats, 1_000);
  }, [loadDiffStats, selectedId, loadChannels]);
  useEffect(() => () => { if (debounceRef.current) clearTimeout(debounceRef.current); }, []);
  useEventStream({ channelId: selectedId, onEvent: onAppEvent });

  // Update window title based on selected channel/thread.
  useEffect(() => {
    if (!selectedId) {
      document.title = "Loop";
      return;
    }
    const selected = channels.find((c) => c.id === selectedId);
    if (!selected) {
      document.title = "Loop";
      return;
    }
    if (selected.parent_id) {
      const parent = channels.find((c) => c.id === selected.parent_id);
      document.title = parent
        ? `${parent.name} › ${selected.name} — Loop`
        : `${selected.name} — Loop`;
    } else {
      document.title = `${selected.name} — Loop`;
    }
  }, [selectedId, channels]);

  const handleSelect = useCallback((id: string | null) => {
    setScrollToMessageId(null);
    setSelectedId((prev) => {
      // Re-clicking the same channel increments mountKey to force re-mount.
      if (id !== null && id === prev) {
        setMountKey((k) => k + 1);
      }
      return id;
    });
    setViewMode(loadMode(id));
    setShellOpen(loadShellOpen(id));
    setShellMaximized(false);
    setAgentRunning(true);
    setAgentKillSignal(0);
  }, []);

  // Auto-select DM channel if nothing is selected on first load.
  const autoSelectedRef = useRef(false);
  useEffect(() => {
    if (autoSelectedRef.current || selectedId || getHashChannelId()) return;
    const dm = channels.find((c) => c.name === "dm" && !c.parent_id);
    if (dm) {
      autoSelectedRef.current = true;
      handleSelect(dm.id);
    }
  }, [channels, selectedId, handleSelect]);

  const handleSelectMessage = useCallback((channelId: string, messageId: number) => {
    setScrollToMessageId(messageId);
    setSelectedId((prev) => {
      if (prev === channelId) {
        // Already on this channel — force re-mount so ChatView picks up the scrollToMessageId.
        setMountKey((k) => k + 1);
      }
      return channelId;
    });
    setViewMode("chat");
  }, []);

  const handleModeChange = useCallback(
    (mode: ViewMode) => {
      setViewMode((prev) => {
        // When switching between terminal-showing modes (or re-clicking the same one),
        // bump mountKey so the Terminal remounts. This restarts a killed session while
        // a running session is seamlessly reattached via the persisted session ID.
        if (mode !== "chat" && prev !== "chat") {
          setMountKey((k) => k + 1);
        }
        return mode;
      });
      if (selectedId) saveMode(selectedId, mode);
    },
    [selectedId],
  );

  const handleCreateChannel = useCallback(async (name: string) => {
    setError(null);
    try {
      const channelId = await createChannel(name);
      await loadChannels();
      handleSelect(channelId);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to create channel";
      setError(message);
      console.error("create channel failed:", err);
    }
  }, [loadChannels, handleSelect]);

  const handleCreateThread = useCallback(
    async (parentId: string, name: string) => {
      setError(null);
      try {
        const threadId = await createThread(parentId, name);
        await loadChannels();
        handleSelect(threadId);
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to create thread";
        setError(message);
        console.error("create thread failed:", err);
      }
    },
    [loadChannels, handleSelect],
  );

  const handleDelete = useCallback(
    async (id: string) => {
      setError(null);
      try {
        const ch = channels.find((c) => c.id === id);
        if (ch && ch.parent_id) {
          await deleteThread(id);
        } else {
          await deleteChannel(id);
        }
        await loadChannels();
        if (selectedId === id) {
          setSelectedId(null);
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to delete";
        setError(message);
        console.error("delete failed:", err);
      }
    },
    [channels, loadChannels, selectedId],
  );

  const handleDeleteBatch = useCallback(
    async (ids: string[]) => {
      setError(null);
      try {
        for (const id of ids) {
          const ch = channels.find((c) => c.id === id);
          if (ch && ch.parent_id) {
            await deleteThread(id);
          } else {
            await deleteChannel(id);
          }
          if (selectedId === id) {
            setSelectedId(null);
          }
        }
        await loadChannels();
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to delete";
        setError(message);
        console.error("batch delete failed:", err);
      }
    },
    [channels, loadChannels, selectedId],
  );

  return (
    <div
      style={{
        display: "flex",
        height: "100vh",
        backgroundColor: colors.bg,
        color: colors.text,
        fontFamily: fonts.sans,
        position: "relative",
      }}
    >
      {/* Branding — always pinned to top-right corner */}
      <span
        style={{
          position: "absolute",
          top: 10,
          right: 12,
          fontSize: 13,
          fontWeight: 600,
          color: colors.textDim,
          letterSpacing: 1,
          display: "flex",
          alignItems: "center",
          gap: 4,
          zIndex: 10,
          pointerEvents: "none",
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 12c-2-2.67-4-4-6-4a4 4 0 1 0 0 8c2 0 4-1.33 6-4Zm0 0c2 2.67 4 4 6 4a4 4 0 0 0 0-8c-2 0-4 1.33-6 4Z" />
        </svg>
        Loop
      </span>
      <Sidebar
        channels={channels}
        selectedId={selectedId}
        collapsed={!sidebarOpen}
        onSelect={handleSelect}
        onCreateChannel={handleCreateChannel}
        onCreateThread={handleCreateThread}
        onDeleteThread={handleDelete}
        onDeleteBatch={handleDeleteBatch}
        onOpenSettings={() => { setSettingsOpen(true); setSettingsDirPath(null); setDiffOpen(false); setDiffMaximized(false); setShellOpen(false); setShellMaximized(false); }}
        onOpenConfig={(dirPath) => { setSettingsOpen(true); setSettingsDirPath(dirPath); setDiffOpen(false); setDiffMaximized(false); setShellOpen(false); setShellMaximized(false); }}
      />
      <div style={{ flex: 1, minWidth: (diffMaximized || shellMaximized) ? 0 : 360, display: (diffMaximized || shellMaximized) ? "none" : "flex", flexDirection: "column" }}>
        {/* Drag region for macOS hiddenInset title bar — enables double-click to zoom */}
        <div
          style={{
            height: 38,
            flexShrink: 0,
            display: "flex",
            alignItems: "center",
            paddingLeft: sidebarOpen ? 4 : 76,
            // @ts-expect-error: WebKit-specific CSS property for Electron drag region
            WebkitAppRegion: "drag",
          }}
        >
          <button
            onClick={() => setSidebarOpen((v) => !v)}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
          <button
            onClick={() => setPaletteOpen(true)}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              marginLeft: 6,
              // @ts-expect-error: WebKit-specific CSS property
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
          <div style={{ flex: 1 }} />
        </div>
        {error && (
          <div
            role="alert"
            style={{
              padding: "8px 12px",
              backgroundColor: "#3b1616",
              color: "#fca5a5",
              fontSize: "13px",
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
            }}
          >
            <span>{error}</span>
            <button
              onClick={() => setError(null)}
              style={{
                background: "none",
                border: "none",
                color: "#fca5a5",
                cursor: "pointer",
                fontSize: "16px",
              }}
            >
              &times;
            </button>
          </div>
        )}
        {selectedId && (
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              alignItems: "center",
              padding: "3px 8px",
              borderBottom: `1px solid ${colors.border}`,
              height: 39,
              boxSizing: "border-box",
            }}
          >
            <span
              style={{
                fontSize: 12,
                color: colors.textDim,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                minWidth: 0,
                display: "flex",
                alignItems: "center",
                gap: 6,
              }}
            >
              {channels.find((c) => c.id === selectedId)?.dir_path || ""}
              {channels.find((c) => c.id === selectedId)?.branch && (
                <>
                <span style={{ color: colors.border, flexShrink: 0 }}>|</span>
                <span
                  style={{
                    fontSize: 11,
                    color: colors.active,
                    fontFamily: fonts.mono,
                    flexShrink: 0,
                  }}
                >
                  <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2, verticalAlign: -1 }}>
                    <line x1="6" y1="3" x2="6" y2="15" />
                    <circle cx="18" cy="6" r="3" />
                    <circle cx="6" cy="18" r="3" />
                    <path d="M18 9a9 9 0 0 1-9 9" />
                  </svg>
                  {channels.find((c) => c.id === selectedId)?.branch}
                </span>
                </>
              )}
            </span>
            <div style={{ display: "flex", alignItems: "stretch", gap: 8 }}>
              <ModeToggle mode={viewMode} onChange={handleModeChange} />
              <button
                onClick={() => setShellOpen((v) => { const next = !v; if (next) { setDiffOpen(false); setDiffMaximized(false); setSettingsOpen(false); } else { setShellMaximized(false); } if (selectedId) saveShellOpen(selectedId, next); return next; })}
                title="Toggle shell terminal"
                style={{
                  background: shellOpen ? colors.selectedBg : "none",
                  border: `1px solid ${shellOpen ? colors.textDim : colors.border}`,
                  color: shellOpen ? colors.textLight : colors.textDim,
                  cursor: "pointer",
                  padding: "2px 6px",
                  fontSize: 10,
                  fontFamily: fonts.mono,
                  lineHeight: 1,
                  borderRadius: 6,
                  display: "flex",
                  alignItems: "center",
                  gap: 4,
                }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4 17 10 11 4 5" />
                  <line x1="12" y1="19" x2="20" y2="19" />
                </svg>
                Shell
              </button>
              <button
                onClick={() => setDiffOpen((v) => { if (!v) { setShellOpen(false); setShellMaximized(false); setSettingsOpen(false); } return !v; })}
                title="Toggle diff panel"
                style={{
                  background: diffOpen ? colors.selectedBg : "none",
                  border: `1px solid ${diffOpen ? colors.textDim : colors.border}`,
                  color: diffOpen ? colors.textLight : colors.textDim,
                  cursor: "pointer",
                  padding: "2px 6px",
                  fontSize: 10,
                  fontFamily: fonts.mono,
                  lineHeight: 1,
                  borderRadius: 6,
                  display: "flex",
                  alignItems: "center",
                  gap: 4,
                }}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 3v18" />
                  <path d="M8 8l-4 4 4 4" />
                  <path d="M16 8l4 4-4 4" />
                </svg>
                {(diffStats.add > 0 || diffStats.del > 0) ? (
                  <>
                    <span style={{ color: "#86efac" }}>+{diffStats.add}</span>
                    <span style={{ color: "#fca5a5" }}>-{diffStats.del}</span>
                  </>
                ) : "Diff"}
              </button>
            </div>
          </div>
        )}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
          <div key="chat-pane" style={{
            flex: viewMode === "chat" ? 1 : undefined,
            display: viewMode === "terminal" ? "none" : "flex",
            flexDirection: "column", overflow: "hidden", minHeight: 0,
          }}>
            <ChatView key={`chat-${selectedId}-${mountKey}`} channelId={selectedId} initialRunningBot={channels.find((c) => c.id === selectedId)?.agent_running} scrollToMessageId={scrollToMessageId} onScrollComplete={() => setScrollToMessageId(null)} />
          </div>
          {viewMode !== "chat" && (
            <div key="term-pane" style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>
              <div style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "0 8px",
                borderBottom: `1px solid ${colors.border}`,
                flexShrink: 0,
                height: 28,
              }}>
                <span style={{ fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
                  Agent Terminal
                </span>
                <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
                  {agentRunning ? (
                    <button
                      onClick={() => setAgentKillSignal((k) => k + 1)}
                      title="Kill all sessions and remove container"
                      style={{
                        padding: "1px 8px",
                        borderRadius: 4,
                        border: `1px solid ${colors.error}`,
                        backgroundColor: "transparent",
                        color: colors.error,
                        cursor: "pointer",
                        fontSize: 10,
                      }}
                    >
                      Kill
                    </button>
                  ) : (
                    <button
                      onClick={() => { setAgentRunning(true); setMountKey((k) => k + 1); }}
                      title="Start new sessions"
                      style={{
                        padding: "1px 8px",
                        borderRadius: 4,
                        border: `1px solid ${colors.active}`,
                        backgroundColor: "transparent",
                        color: colors.active,
                        cursor: "pointer",
                        fontSize: 10,
                      }}
                    >
                      Restart
                    </button>
                  )}
                  <div style={{ position: "relative" }}>
                    <button
                      onClick={() => setAgentSplitMenu((v) => !v)}
                      title="Split pane"
                      style={{
                        background: "none",
                        border: "none",
                        color: colors.textDim,
                        cursor: "pointer",
                        padding: 4,
                        lineHeight: 1,
                        borderRadius: 4,
                        display: "flex",
                        alignItems: "center",
                      }}
                      onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                      onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
                    >
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <line x1="12" y1="5" x2="12" y2="19" />
                        <line x1="5" y1="12" x2="19" y2="12" />
                      </svg>
                    </button>
                    {agentSplitMenu && (
                      <SplitMenu
                        onSplit={(dir) => { setAgentSplitMenu(false); agentPanesRef.current?.splitLast(dir); }}
                        onClose={() => setAgentSplitMenu(false)}
                      />
                    )}
                  </div>
                </div>
              </div>
              <TerminalPanes ref={agentPanesRef} key={`term-${selectedId}-${mountKey}`} channelId={selectedId} target="agent" storageKey="loop-agent-panes" mountKey={mountKey} hideActions killSignal={agentKillSignal} onStatusChange={loadChannels} onRunningChange={setAgentRunning} />
            </div>
          )}
        </div>
      </div>
      {diffOpen && selectedId && (
        <DiffPanel
          channelId={selectedId}
          dirPath={channels.find((c) => c.id === selectedId)?.dir_path || ""}
          branch={channels.find((c) => c.id === selectedId)?.branch || ""}
          maximized={diffMaximized}
          sidebarOpen={sidebarOpen}
          onToggleSidebar={() => setSidebarOpen((v) => !v)}
          onOpenPalette={() => setPaletteOpen(true)}
          onToggleMaximize={() => setDiffMaximized((v) => !v)}
          onClose={() => { setDiffOpen(false); setDiffMaximized(false); }}
        />
      )}
      {shellOpen && selectedId && (
        <ShellPanel
          channelId={selectedId}
          dirPath={channels.find((c) => c.id === selectedId)?.dir_path || ""}
          branch={channels.find((c) => c.id === selectedId)?.branch || ""}
          maximized={shellMaximized}
          sidebarOpen={sidebarOpen}
          onToggleSidebar={() => setSidebarOpen((v) => !v)}
          onOpenPalette={() => setPaletteOpen(true)}
          onToggleMaximize={() => setShellMaximized((v) => !v)}
          onClose={() => { setShellOpen(false); setShellMaximized(false); if (selectedId) saveShellOpen(selectedId, false); }}
        />
      )}
      {settingsOpen && (
        <Settings open={settingsOpen} projectDirPath={settingsDirPath} onClose={() => setSettingsOpen(false)} onDaemonRestarted={loadChannels} />
      )}
      <CommandPalette
        channels={channels}
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        onSelect={handleSelect}
        onSelectMessage={handleSelectMessage}
      />
    </div>
  );
}
