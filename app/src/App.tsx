import { useCallback, useEffect, useState } from "react";
import type { Channel } from "./types";
import { colors, fonts } from "./theme";
import { createThread, fetchChannels, initApiUrl } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { Terminal } from "./components/Terminal";
import { ChatView } from "./components/ChatView";

type ViewMode = "terminal" | "chat";

export default function App() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<ViewMode>("terminal");
  const [ready, setReady] = useState(false);

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

  const handleCreateThread = useCallback(
    async (parentId: string, name: string) => {
      try {
        const threadId = await createThread(parentId, name);
        await loadChannels();
        setSelectedId(threadId);
      } catch {
        /* TODO: surface error to user */
      }
    },
    [loadChannels],
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
        onSelect={setSelectedId}
        onCreateThread={handleCreateThread}
      />
      <div style={{ display: "flex", flexDirection: "column", flex: 1 }}>
        <ViewModeToggle mode={viewMode} onToggle={setViewMode} />
        {viewMode === "terminal" ? (
          <Terminal
            channelId={selectedId}
            containerId={
              channels.find((c) => c.id === selectedId)?.container_id ?? null
            }
          />
        ) : (
          <ChatView channelId={selectedId} />
        )}
      </div>
    </div>
  );
}

function ViewModeToggle({
  mode,
  onToggle,
}: {
  mode: ViewMode;
  onToggle: (mode: ViewMode) => void;
}) {
  return (
    <div style={toggleStyles.bar}>
      <button
        onClick={() => onToggle("terminal")}
        style={{
          ...toggleStyles.button,
          ...(mode === "terminal" ? toggleStyles.active : {}),
        }}
      >
        Terminal
      </button>
      <button
        onClick={() => onToggle("chat")}
        style={{
          ...toggleStyles.button,
          ...(mode === "chat" ? toggleStyles.active : {}),
        }}
      >
        Chat
      </button>
    </div>
  );
}

const toggleStyles: Record<string, React.CSSProperties> = {
  bar: {
    display: "flex",
    gap: 2,
    padding: "4px 8px",
    borderBottom: `1px solid ${colors.border}`,
    backgroundColor: colors.surface,
  },
  button: {
    padding: "4px 12px",
    fontSize: 12,
    fontFamily: fonts.sans,
    fontWeight: 500,
    color: colors.textMuted,
    backgroundColor: "transparent",
    border: "none",
    borderRadius: 4,
    cursor: "pointer",
  },
  active: {
    color: colors.textLight,
    backgroundColor: colors.selectedBg,
  },
};
