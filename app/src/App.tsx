import { useCallback, useEffect, useRef, useState } from "react";
import type { Channel, WSEvent } from "./types";
import { colors, fonts } from "./theme";
import { createChannel, createThread, deleteChannel, deleteThread, fetchChannels, fetchDiff, initApiUrl, killAgentContainer } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { ChatView } from "./components/ChatView";
import { DiffPanel } from "./components/DiffPanel";
import { MarkdownFilePanel } from "./components/FilePanel";
import { MemoryPanel } from "./components/MemoryPanel";
import { TerminalPanel } from "./components/TerminalPanel";
import { CommandPalette } from "./components/CommandPalette";
import { Settings } from "./components/Settings";
import { useEventStream } from "./hooks/useEventStream";

const TERMINAL_OPEN_KEY = "loop-terminal-open";

function loadTerminalOpen(channelId: string | null): boolean {
  if (!channelId) return false;
  try {
    const stored = localStorage.getItem(TERMINAL_OPEN_KEY);
    if (stored) {
      const prefs = JSON.parse(stored);
      if (typeof prefs === "object" && prefs !== null) {
        return prefs[channelId] === true;
      }
    }
  } catch { /* ignore */ }
  return false;
}

function saveTerminalOpen(channelId: string, open: boolean) {
  try {
    const stored = localStorage.getItem(TERMINAL_OPEN_KEY);
    const parsed = stored ? JSON.parse(stored) : null;
    const prefs: Record<string, boolean> = (typeof parsed === "object" && parsed !== null) ? parsed : {};
    prefs[channelId] = open;
    localStorage.setItem(TERMINAL_OPEN_KEY, JSON.stringify(prefs));
  } catch { /* ignore */ }
}

const TERMINAL_TREE_KEY = "loop-terminal-panes";

function clearTerminalTree(channelId: string) {
  try {
    const stored = localStorage.getItem(TERMINAL_TREE_KEY);
    if (stored) {
      const all = JSON.parse(stored);
      if (typeof all === "object" && all !== null) {
        delete all[channelId];
        localStorage.setItem(TERMINAL_TREE_KEY, JSON.stringify(all));
      }
    }
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
  const [terminalOpen, setTerminalOpen] = useState(() => loadTerminalOpen(getHashChannelId()));
  const [terminalMaximized, setTerminalMaximized] = useState(false);
  const [readmeOpen, setReadmeOpen] = useState(false);
  const [readmeMaximized, setReadmeMaximized] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [memoryMaximized, setMemoryMaximized] = useState(false);

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
      const termOpen = loadTerminalOpen(id);
      setTerminalOpen(termOpen);
      setTerminalMaximized(false);
      setDiffOpen(false); setDiffMaximized(false);
      setMemoryOpen(false); setMemoryMaximized(false);
      setReadmeOpen(false); setReadmeMaximized(false);
      setSettingsOpen(false);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  // Handle deep link from protocol URL (loop://channel/<id>).
  useEffect(() => {
    if (window.loopAPI?.onNavigateChannel) {
      window.loopAPI.onNavigateChannel((channelId: string) => {
        setSelectedId(channelId);
        const termOpen = loadTerminalOpen(channelId);
        setTerminalOpen(termOpen);
        setTerminalMaximized(false);
        setDiffOpen(false); setDiffMaximized(false);
        setMemoryOpen(false); setMemoryMaximized(false);
        setReadmeOpen(false); setReadmeMaximized(false);
        setSettingsOpen(false);
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
          if (!v) { setDiffOpen(false); setDiffMaximized(false); setTerminalOpen(false); setTerminalMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setMemoryOpen(false); setMemoryMaximized(false); }
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
      window.loopAPI.onOpenSettings(() => { setSettingsOpen(true); setSettingsDirPath(null); setDiffOpen(false); setDiffMaximized(false); setTerminalOpen(false); setTerminalMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setMemoryOpen(false); setMemoryMaximized(false); });
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
    const termOpen = loadTerminalOpen(id);
    setTerminalOpen(termOpen);
    setTerminalMaximized(false);
    setDiffOpen(false); setDiffMaximized(false);
    setMemoryOpen(false); setMemoryMaximized(false);
    setReadmeOpen(false); setReadmeMaximized(false);
    setSettingsOpen(false);
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
  }, []);

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
        onOpenSettings={() => { setSettingsOpen(true); setSettingsDirPath(null); setDiffOpen(false); setDiffMaximized(false); setTerminalOpen(false); setTerminalMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setMemoryOpen(false); setMemoryMaximized(false); }}
        onOpenConfig={(dirPath) => { setSettingsOpen(true); setSettingsDirPath(dirPath); setDiffOpen(false); setDiffMaximized(false); setTerminalOpen(false); setTerminalMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setMemoryOpen(false); setMemoryMaximized(false); }}
      />
      <div style={{ flex: 1, minWidth: (diffMaximized || terminalMaximized || readmeMaximized || memoryMaximized) ? 0 : 360, display: (diffMaximized || terminalMaximized || readmeMaximized || memoryMaximized) ? "none" : "flex", flexDirection: "column" }}>
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
          <button
            onClick={() => setReadmeOpen((v) => { if (!v) { setDiffOpen(false); setDiffMaximized(false); setTerminalOpen(false); setTerminalMaximized(false); setSettingsOpen(false); setMemoryOpen(false); setMemoryMaximized(false); } else { setReadmeMaximized(false); } return !v; })}
            title="README"
            style={{
              background: readmeOpen ? colors.selectedBg : "none",
              border: `1px solid ${readmeOpen ? colors.textDim : colors.border}`,
              color: readmeOpen ? colors.textLight : colors.textDim,
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
              <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
              <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
            </svg>
            README
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
              <button
                onClick={() => setMemoryOpen((v) => { if (!v) { setTerminalOpen(false); setTerminalMaximized(false); setDiffOpen(false); setDiffMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setSettingsOpen(false); } else { setMemoryMaximized(false); } return !v; })}
                title="Toggle memory panel"
                style={{
                  background: memoryOpen ? colors.selectedBg : "none",
                  border: `1px solid ${memoryOpen ? colors.textDim : colors.border}`,
                  color: memoryOpen ? colors.textLight : colors.textDim,
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
                  <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                  <polyline points="14 2 14 8 20 8" />
                  <line x1="16" y1="13" x2="8" y2="13" />
                  <line x1="16" y1="17" x2="8" y2="17" />
                </svg>
                Memory
              </button>
              <button
                onClick={() => setTerminalOpen((v) => { const next = !v; if (next) { setDiffOpen(false); setDiffMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setSettingsOpen(false); setMemoryOpen(false); setMemoryMaximized(false); } else { setTerminalMaximized(false); } if (selectedId) saveTerminalOpen(selectedId, next); return next; })}
                title="Toggle terminal panel"
                style={{
                  background: terminalOpen ? colors.selectedBg : "none",
                  border: `1px solid ${terminalOpen ? colors.textDim : colors.border}`,
                  color: terminalOpen ? colors.textLight : colors.textDim,
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
                Terminal
              </button>
              <button
                onClick={() => setDiffOpen((v) => { if (!v) { setTerminalOpen(false); setTerminalMaximized(false); setReadmeOpen(false); setReadmeMaximized(false); setSettingsOpen(false); setMemoryOpen(false); setMemoryMaximized(false); } return !v; })}
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
          <ChatView key={`chat-${selectedId}-${mountKey}`} channelId={selectedId} initialRunningBot={channels.find((c) => c.id === selectedId)?.agent_running} scrollToMessageId={scrollToMessageId} onScrollComplete={() => setScrollToMessageId(null)} />
        </div>
      </div>
      {terminalOpen && selectedId && (
        <TerminalPanel
          channelId={selectedId}
          dirPath={channels.find((c) => c.id === selectedId)?.dir_path || ""}
          branch={channels.find((c) => c.id === selectedId)?.branch || ""}
          maximized={terminalMaximized}
          sidebarOpen={sidebarOpen}
          onToggleSidebar={() => setSidebarOpen((v) => !v)}
          onOpenPalette={() => setPaletteOpen(true)}
          onToggleMaximize={() => setTerminalMaximized((v) => !v)}
          onClose={() => { setTerminalOpen(false); setTerminalMaximized(false); if (selectedId) { saveTerminalOpen(selectedId, false); clearTerminalTree(selectedId); killAgentContainer(selectedId); } }}
          onStatusChange={loadChannels}
        />
      )}
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
      {readmeOpen && (
        <MarkdownFilePanel
          dirPath={selectedId ? channels.find((c) => c.id === selectedId)?.dir_path || "" : ""}
          branch={selectedId ? channels.find((c) => c.id === selectedId)?.branch || "" : ""}
          maximized={readmeMaximized}
          sidebarOpen={sidebarOpen}
          onToggleSidebar={() => setSidebarOpen((v) => !v)}
          onOpenPalette={() => setPaletteOpen(true)}
          onToggleMaximize={() => setReadmeMaximized((v) => !v)}
          onClose={() => { setReadmeOpen(false); setReadmeMaximized(false); }}
        />
      )}
      {memoryOpen && selectedId && (
        <MemoryPanel
          channelId={selectedId}
          dirPath={channels.find((c) => c.id === selectedId)?.dir_path || ""}
          branch={channels.find((c) => c.id === selectedId)?.branch || ""}
          maximized={memoryMaximized}
          sidebarOpen={sidebarOpen}
          onToggleSidebar={() => setSidebarOpen((v) => !v)}
          onOpenPalette={() => setPaletteOpen(true)}
          onToggleMaximize={() => setMemoryMaximized((v) => !v)}
          onClose={() => { setMemoryOpen(false); setMemoryMaximized(false); }}
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
