import { useCallback, useEffect, useRef, useState } from "react";
import type { AppSettings, Channel, UpdateStatus, WSEvent } from "./types";
import { fonts } from "./theme";
import { ThemeProvider, useTheme } from "./ThemeContext";
import { createChannel, createThread, createWorktreeThread, deleteChannel, deleteThread, ensureChannel, fetchChannels, fetchDiff, importWorktree, initApiUrl } from "./api/loopApi";
import { Sidebar } from "./components/Sidebar";
import { MarkdownFilePanel } from "./components/FilePanel";
import { WorkspaceLayout, type WorkspaceLayoutRef } from "./components/WorkspaceLayout";
import { LoopLogo } from "./components/LoopLogo";
import horizontalLogo from "./assets/logo-horizontal.svg";
import { CommandPalette } from "./components/CommandPalette";
import { Settings } from "./components/Settings";
import { useChatStateStore, type ActiveChatState } from "./hooks/useChatStateStore";

function getHashChannelId(): string | null {
  const hash = window.location.hash.slice(1);
  return hash || null;
}

export default function App() {
  const [settings, setSettings] = useState<AppSettings | null>(null);

  useEffect(() => {
    const defaults: AppSettings = { stopDaemonOnQuit: false, autoSaveOnBlur: true, previewTabs: true };
    const p = window.loopAPI?.getSettings?.();
    if (p) {
      p.then((s) => setSettings(s)).catch(() => setSettings(defaults));
    } else {
      setSettings(defaults);
    }
  }, []);

  // Wait for settings to load before rendering so we don't flash wrong theme.
  if (!settings) return null;

  return (
    <ThemeProvider initialTheme={settings.theme}>
      <AppInner />
    </ThemeProvider>
  );
}

function AppInner() {
  const { colors } = useTheme();
  const [channels, setChannels] = useState<Channel[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(getHashChannelId);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [mountKey, setMountKey] = useState(0);
  const [diffStats, setDiffStats] = useState<{ add: number; del: number }>({ add: 0, del: 0 });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsDirPath, setSettingsDirPath] = useState<string | null>(null);
  const [scrollToMessageId, setScrollToMessageId] = useState<number | null>(null);
  const [readmeOpen, setReadmeOpen] = useState(false);
  const [openMemoryFile, setOpenMemoryFile] = useState<string | null>(null);
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null);

  const layoutRef = useRef<WorkspaceLayoutRef>(null);

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
      setSelectedId(getHashChannelId());
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  // Handle deep link from protocol URL (loop://channel/<id>).
  useEffect(() => {
    if (window.loopAPI?.onNavigateChannel) {
      window.loopAPI.onNavigateChannel((channelId: string) => {
        setSelectedId(channelId);
      });
    }
  }, []);

  // Cmd+K / Ctrl+K to toggle command palette, Cmd+, for settings, Cmd+E for editor layout.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setPaletteOpen((v) => !v);
      }
      if ((e.metaKey || e.ctrlKey) && e.key === ",") {
        e.preventDefault();
        setSettingsOpen((v) => {
          if (!v) setReadmeOpen(false);
          return !v;
        });
        setSettingsDirPath(null);
      }
      if ((e.metaKey || e.ctrlKey) && e.key === "e") {
        e.preventDefault();
        layoutRef.current?.switchToLayout("Editor");
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // Listen for Settings menu item from main process.
  useEffect(() => {
    if (window.loopAPI?.onOpenSettings) {
      window.loopAPI.onOpenSettings(() => { setReadmeOpen(false); setSettingsOpen(true); setSettingsDirPath(null); });
    }
  }, []);

  // Auto-updater state.
  useEffect(() => {
    window.loopAPI?.getUpdateStatus?.().then(setUpdateStatus);
    window.loopAPI?.onUpdateStatus?.((status) => setUpdateStatus(status));
  }, []);

  const handleDownloadUpdate = useCallback(async () => {
    try { await window.loopAPI?.downloadUpdate?.(); } catch (e) { console.warn("download update failed:", e); }
  }, []);

  const handleInstallUpdate = useCallback(() => {
    window.loopAPI?.installUpdate?.();
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

  // Single app-level WS that subscribes to the selected channel + all running
  // channels. Replaces the previous dual-WS approach (one in App, one in
  // WorkspaceLayout). Chat state is persisted in the store across switches.
  const { getState, saveState, isRunningMapRef, unreadIdsRef, unreadCount, markRead, markAllRead, subscribeChatEvents } = useChatStateStore({
    channels,
    selectedId,
    onAppEvent,
  });

  const handleChatStateUnmount = useCallback(
    (channelId: string, state: ActiveChatState) => saveState(channelId, state),
    [saveState],
  );

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
    setReadmeOpen(false);
    setSettingsOpen(false);
    if (id) markRead(id);
    setSelectedId((prev) => {
      // Re-clicking the same channel increments mountKey to force re-mount.
      if (id !== null && id === prev) {
        setMountKey((k) => k + 1);
      }
      return id;
    });
  }, [markRead]);

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

  const handleSelectMemoryFile = useCallback((filePath: string) => {
    setOpenMemoryFile(filePath);
    // Switch to a layout that has a memory pane.
    layoutRef.current?.switchToLayout("Memory");
  }, []);

  const handleOpenDirectory = useCallback(async (dirPath: string) => {
    setError(null);
    try {
      // Run onboard:local to set up .mcp.json, .loop/config.json, templates
      const result = await window.loopAPI?.onboardLocal?.(dirPath);
      if (result && !result.ok) {
        console.warn("onboard:local failed:", result.error);
      }
      const channel = await ensureChannel(dirPath);
      await loadChannels();
      handleSelect(channel.id);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to open directory";
      setError(message);
      console.error("open directory failed:", err);
    }
  }, [loadChannels, handleSelect]);

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

  const handleCreateWorktree = useCallback(
    async (channelId: string, branch: string) => {
      setError(null);
      try {
        const { threadId } = await createWorktreeThread(channelId, branch);
        await loadChannels();
        handleSelect(threadId);
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to create worktree";
        setError(message);
        console.error("create worktree failed:", err);
      }
    },
    [loadChannels, handleSelect],
  );

  const handleImportWorktree = useCallback(
    async (channelId: string, worktreePath: string) => {
      setError(null);
      try {
        const { threadId } = await importWorktree(channelId, worktreePath);
        await loadChannels();
        handleSelect(threadId);
      } catch (err) {
        const message = err instanceof Error ? err.message : "Failed to import worktree";
        setError(message);
        console.error("import worktree failed:", err);
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

  const selectedChannel = channels.find((c) => c.id === selectedId);
  const selectedDirPath = selectedChannel?.dir_path || "";
  const selectedBranch = selectedChannel?.branch || "";

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
      <img
        src={horizontalLogo}
        alt="Loop"
        width={52}
        style={{
          position: "absolute",
          top: 12,
          right: 12,
          zIndex: 10,
          pointerEvents: "none",
          // @ts-expect-error: WebKit-specific CSS property for Electron drag region
          WebkitAppRegion: "drag",
        }}
      />
      <Sidebar
        channels={channels}
        selectedId={selectedId}
        collapsed={!sidebarOpen}
        onSelect={handleSelect}
        onOpenDirectory={handleOpenDirectory}
        onCreateChannel={handleCreateChannel}
        onCreateThread={handleCreateThread}
        onDeleteThread={handleDelete}
        onDeleteBatch={handleDeleteBatch}
        onOpenSettings={() => { setReadmeOpen(false); setSettingsOpen((v) => !v); setSettingsDirPath(null); }}
        onOpenConfig={(dirPath) => { setReadmeOpen(false); setSettingsOpen((v) => { if (v && settingsDirPath === dirPath) return false; setSettingsDirPath(dirPath); return true; }); }}
        onOpenReadme={() => { setSettingsOpen(false); setReadmeOpen((v) => !v); }}
        updateStatus={updateStatus}
        onDownloadUpdate={handleDownloadUpdate}
        onInstallUpdate={handleInstallUpdate}
        isRunningMapRef={isRunningMapRef}
        unreadIdsRef={unreadIdsRef}
        unreadCount={unreadCount}
        onMarkAllRead={markAllRead}
      />
      {selectedId && selectedChannel ? (
        <>
          <WorkspaceLayout
            ref={layoutRef}
            key={`layout-${selectedId}-${mountKey}`}
            channelId={selectedId}
            channel={selectedChannel}
            sidebarOpen={sidebarOpen}
            style={readmeOpen || settingsOpen ? { display: "none" } : undefined}
            onToggleSidebar={() => setSidebarOpen((v) => !v)}
            onOpenPalette={() => setPaletteOpen(true)}
            scrollToMessageId={scrollToMessageId}
            onScrollComplete={() => setScrollToMessageId(null)}
            openMemoryFile={openMemoryFile}
            onOpenMemoryFileComplete={() => setOpenMemoryFile(null)}
            onStatusChange={loadChannels}
            error={error}
            onDismissError={() => setError(null)}
            diffStats={diffStats}
            onCreateWorktree={handleCreateWorktree}
            onImportWorktree={handleImportWorktree}
            onSelectThread={handleSelect}
            initialChatState={selectedId ? getState(selectedId) : undefined}
            onChatStateUnmount={handleChatStateUnmount}
            subscribeChatEvents={subscribeChatEvents}
          />
          {readmeOpen && (
            <MarkdownFilePanel
              dirPath={selectedDirPath}
              branch={selectedBranch}
              maximized
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => setReadmeOpen(false)}
            />
          )}
          {settingsOpen && (
            <Settings
              open={settingsOpen}
              projectDirPath={settingsDirPath}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => setSettingsOpen(false)}
              onDaemonRestarted={loadChannels}
            />
          )}
        </>
      ) : (
        <>
          {settingsOpen ? (
            <Settings
              open={settingsOpen}
              projectDirPath={settingsDirPath}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => setSettingsOpen(false)}
              onDaemonRestarted={loadChannels}
            />
          ) : (
            <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
              <LoopLogo />
            </div>
          )}
        </>
      )}
      <CommandPalette
        channels={channels}
        selectedChannelId={selectedId}
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        onSelect={handleSelect}
        onSelectMessage={handleSelectMessage}
        onSelectMemoryFile={handleSelectMemoryFile}
      />
    </div>
  );
}
