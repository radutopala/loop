import { useCallback, useEffect, useState } from "react";
import type { Channel } from "./types";
import { createThread, fetchChannels, initApiUrl } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { Terminal } from "./components/Terminal";

export default function App() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
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
        backgroundColor: "#1a1b26",
        color: "#a9b1d6",
        fontFamily:
          "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
      }}
    >
      <Sidebar
        channels={channels}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onCreateThread={handleCreateThread}
      />
      <Terminal
        channelId={selectedId}
        containerId={
          channels.find((c) => c.id === selectedId)?.container_id ?? null
        }
      />
    </div>
  );
}
