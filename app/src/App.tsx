import { useCallback, useEffect, useRef, useState } from "react";
import type { Channel, ViewMode } from "./types";
import { colors, fonts } from "./theme";
import { createChannel, createThread, deleteChannel, deleteThread, fetchChannels, fetchDiff, initApiUrl } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { Terminal } from "./components/Terminal";
import { ChatView } from "./components/ChatView";
import { ModeToggle } from "./components/ModeToggle";
import { DiffPanel } from "./components/DiffPanel";
import { useEventStream } from "./hooks/useEventStream";

const MODE_STORAGE_KEY = "loop-view-mode";

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
  const [diffStats, setDiffStats] = useState<{ add: number; del: number }>({ add: 0, del: 0 });

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
  const onDiffEvent = useCallback(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(loadDiffStats, 1_000);
  }, [loadDiffStats]);
  useEffect(() => () => { if (debounceRef.current) clearTimeout(debounceRef.current); }, []);
  useEventStream({ channelId: selectedId, onEvent: onDiffEvent });

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
      });
    }
  }, []);



  const loadChannels = useCallback(async () => {
    if (!ready) return;
    try {
      setChannels(await fetchChannels());
    } catch {
      /* will retry on next poll */
    }
  }, [ready]);

  useEffect(() => {
    loadChannels();
    const id = setInterval(loadChannels, 30_000);
    return () => clearInterval(id);
  }, [loadChannels]);

  const handleSelect = useCallback((id: string | null) => {
    setSelectedId((prev) => {
      // Re-clicking the same channel increments mountKey to force re-mount
      // (e.g. re-attach after detach).
      if (id !== null && id === prev) {
        setMountKey((k) => k + 1);
      }
      return id;
    });
    setViewMode(loadMode(id));
  }, []);

  const handleModeChange = useCallback(
    (mode: ViewMode) => {
      setViewMode(mode);
      if (selectedId) saveMode(selectedId, mode);
    },
    [selectedId],
  );

  const handleCreateChannel = useCallback(async () => {
    setError(null);
    try {
      const name = `chat-${Date.now().toString(36)}`;
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

  return (
    <div
      style={{
        display: "flex",
        height: "100vh",
        backgroundColor: colors.bg,
        color: colors.text,
        fontFamily: fonts.sans,
      }}
    >
      <Sidebar
        channels={channels}
        selectedId={selectedId}
        collapsed={!sidebarOpen}
        onSelect={handleSelect}
        onCreateChannel={handleCreateChannel}
        onCreateThread={handleCreateThread}
        onDeleteThread={handleDelete}
      />
      <div style={{ flex: 1, minWidth: 360, display: "flex", flexDirection: "column" }}>
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
              padding: "4px 8px",
              borderBottom: `1px solid ${colors.border}`,
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
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <ModeToggle mode={viewMode} onChange={handleModeChange} />
              <button
                onClick={() => setDiffOpen((v) => !v)}
                title="Toggle diff panel"
                style={{
                  background: diffOpen ? colors.selectedBg : "none",
                  border: `1px solid ${diffOpen ? colors.textDim : colors.border}`,
                  color: diffOpen ? colors.textLight : colors.textDim,
                  cursor: "pointer",
                  padding: "3px 8px",
                  fontSize: 11,
                  fontFamily: fonts.mono,
                  lineHeight: 1,
                  borderRadius: 4,
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
        {viewMode === "terminal" ? (
          <Terminal key={`${selectedId}-${mountKey}`} channelId={selectedId} onStatusChange={loadChannels} />
        ) : (
          <ChatView key={`${selectedId}-${mountKey}`} channelId={selectedId} initialRunningBot={channels.find((c) => c.id === selectedId)?.agent_running} />
        )}
      </div>
      {diffOpen && selectedId && (
        <DiffPanel channelId={selectedId} onClose={() => setDiffOpen(false)} />
      )}
    </div>
  );
}
