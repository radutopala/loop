import { useCallback, useEffect, useState } from "react";
import type { Channel, ViewMode } from "./types";
import { colors, fonts } from "./theme";
import { createThread, fetchChannels, initApiUrl } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { Terminal } from "./components/Terminal";
import { ChatView } from "./components/ChatView";
import { ModeToggle } from "./components/ModeToggle";

const MODE_STORAGE_KEY = "loop-view-mode";

function loadMode(channelId: string | null): ViewMode {
  if (!channelId) return "terminal";
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
  return "terminal";
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

export default function App() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [viewMode, setViewMode] = useState<ViewMode>("terminal");

  useEffect(() => {
    initApiUrl().then(() => setReady(true));
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
    setSelectedId(id);
    setViewMode(loadMode(id));
  }, []);

  const handleModeChange = useCallback(
    (mode: ViewMode) => {
      setViewMode(mode);
      if (selectedId) saveMode(selectedId, mode);
    },
    [selectedId],
  );

  const handleCreateThread = useCallback(
    async (parentId: string, name: string) => {
      try {
        const threadId = await createThread(parentId, name);
        await loadChannels();
        handleSelect(threadId);
      } catch {
        /* TODO: surface error to user */
      }
    },
    [loadChannels, handleSelect],
  );

  const containerId =
    channels.find((c) => c.id === selectedId)?.container_id ?? null;

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
        onSelect={handleSelect}
        onCreateThread={handleCreateThread}
      />
      <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
        {selectedId && (
          <div
            style={{
              display: "flex",
              justifyContent: "flex-end",
              padding: "4px 8px",
              borderBottom: `1px solid ${colors.border}`,
            }}
          >
            <ModeToggle mode={viewMode} onChange={handleModeChange} />
          </div>
        )}
        {viewMode === "terminal" ? (
          <Terminal channelId={selectedId} containerId={containerId} />
        ) : (
          <ChatView channelId={selectedId} />
        )}
      </div>
    </div>
  );
}
