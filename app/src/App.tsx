import { useCallback, useEffect, useRef, useState } from "react";
import type { Channel, ImageBuildStatusData, ImageUpdateAvailableData, UpdateStatus, WSEvent } from "./types";
import { fonts } from "./theme";
import { ThemeProvider, useTheme, DEFAULT_FONT_SIZES } from "./ThemeContext";
import { createChannel, createThread, createWorktreeThread, deleteChannel, deleteThread, ensureChannel, fetchChannels, fetchDiff, getImageStatus, importWorktree, initApiUrl, rebuildImage } from "./api/loopApi";
import { fetchGlobalConfig } from "./api/configApi";
import { Sidebar } from "./components/sidebar/Sidebar";
import { MarkdownFilePanel } from "./components/panels/FilePanel";
import { WorkspaceLayout, type WorkspaceLayoutRef } from "./components/layout/WorkspaceLayout";
import { LoopLogo } from "./components/shared/LoopLogo";
import horizontalLogo from "./assets/logo-horizontal.svg";
import { CommandPalette } from "./components/shared/CommandPalette";
import { Settings } from "./components/shared/Settings";
import { ContainersPanel, type ContainersPanelHandle } from "./components/panels/ContainersPanel";
import { GlobalTasksPanel } from "./components/panels/GlobalTasksPanel";
import { WorkflowsGlobalPanel, type WorkflowsGlobalPanelHandle } from "./components/panels/WorkflowsGlobalPanel";
import { useChatStateStore, type ActiveChatState } from "./hooks/useChatStateStore";
import { useAppPanelState } from "./hooks/useAppPanelState";
import { storageGet, storageSet, storageRemove } from "./utils/storage";

const LAST_CHANNEL_KEY = "loop-last-channel";

function getHashChannelId(): string | null {
  const hash = window.location.hash.slice(1);
  if (hash) return hash;
  // Restore last selected channel (Electron loses hash on restart).
  return storageGet(LAST_CHANNEL_KEY);
}

export default function App() {
  const [desktop, setDesktop] = useState<Record<string, any> | null | undefined>(undefined);

  useEffect(() => {
    initApiUrl()
      .then(() => fetchGlobalConfig())
      .then((cfg) => setDesktop(cfg.content?.desktop ?? null))
      .catch(() => setDesktop(null));
  }, []);

  // Wait for config to load before rendering so we don't flash wrong theme.
  if (desktop === undefined) return null;

  return (
    <ThemeProvider
      initialTheme={desktop?.theme}
      initialFontSizes={desktop?.font_sizes ? { ...DEFAULT_FONT_SIZES, ...desktop.font_sizes } : undefined}
      initialIslands={desktop?.islands ?? true}
    >
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
  const {
    settingsOpen, readmeOpen, containersOpen, tasksOpen, workflowsOpen,
    settingsDirPath, configDirty, pendingSelectId,
    setConfigDirty, setPendingSelectId,
    togglePanel, openConfig,
    toggleSettingsKeyboard, forceOpenSettings,
    closePanel, closeAllPanels,
  } = useAppPanelState();
  const [scrollToMessageId, setScrollToMessageId] = useState<number | null>(null);
  const containersPanelRef = useRef<ContainersPanelHandle | null>(null);
  const workflowsPanelRef = useRef<WorkflowsGlobalPanelHandle | null>(null);
  const [openMemoryFile, setOpenMemoryFile] = useState<string | null>(null);
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null);
  const [imageBuildStatus, setImageBuildStatus] = useState<ImageBuildStatusData | null>(null);
  const [imageUpdateAvailable, setImageUpdateAvailable] = useState<ImageUpdateAvailableData | null>(null);

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

  // API URL already initialized by parent App component.
  useEffect(() => { setReady(true); }, []);

  // Seed image build status and update availability from API on mount.
  useEffect(() => {
    if (!ready) return;
    getImageStatus().then((img) => {
      if (img?.status && img.status.state !== "idle") setImageBuildStatus(img.status);
      if (img?.update_available) setImageUpdateAvailable(img.update_available);
    }).catch(() => {});
  }, [ready]);

  // Sync hash and localStorage with selected channel.
  useEffect(() => {
    window.location.hash = selectedId ? selectedId : "";
    if (selectedId) storageSet(LAST_CHANNEL_KEY, selectedId);
    else storageRemove(LAST_CHANNEL_KEY);
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
        toggleSettingsKeyboard();
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
      window.loopAPI.onOpenSettings(() => { forceOpenSettings(); });
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

  const handleRebuildImage = useCallback(async () => {
    try { await rebuildImage(); } catch (e) { console.warn("rebuild image failed:", e); }
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
    if (event.type === "image.build_status") {
      setImageBuildStatus(event.data as ImageBuildStatusData);
      return;
    }
    if (event.type === "image.update_available") {
      setImageUpdateAvailable(event.data as ImageUpdateAvailableData);
      return;
    }
    if (event.type.startsWith("container.")) {
      containersPanelRef.current?.handleContainerEvent(event);
      return;
    }
    if (event.type.startsWith("workflow.")) {
      workflowsPanelRef.current?.handleWorkflowEvent(event);
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

  const doSelect = useCallback((id: string | null) => {
    setScrollToMessageId(null);
    closeAllPanels();
    if (id) markRead(id);
    setSelectedId((prev) => {
      if (id !== null && id === prev) {
        setMountKey((k) => k + 1);
      }
      return id;
    });
  }, [markRead, closeAllPanels]);

  const handleSelect = useCallback((id: string | null) => {
    if (configDirty && settingsOpen) {
      setPendingSelectId(id);
      return;
    }
    doSelect(id);
  }, [configDirty, settingsOpen, doSelect]);

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

  // Derive channel ID/object for Settings when opened from a channel's config button.
  const settingsChannel = settingsDirPath
    ? channels.find((c) => c.dir_path === settingsDirPath && !c.parent_id) ?? null
    : null;
  const settingsChannelId = settingsChannel?.id ?? null;

  return (
    <div
      style={{
        display: "flex",
        height: "100vh",
        backgroundColor: colors.canvas,
        color: colors.text,
        fontFamily: fonts.sans,
        position: "relative",
        padding: colors.islandGap,
        gap: colors.islandGap,
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
        onOpenSettings={() => togglePanel("settings")}
        onOpenConfig={openConfig}
        onOpenReadme={() => togglePanel("readme")}
        onOpenContainers={() => togglePanel("containers")}
        onOpenTasks={() => togglePanel("tasks")}
        onOpenWorkflows={() => togglePanel("workflows")}
        updateStatus={updateStatus}
        onDownloadUpdate={handleDownloadUpdate}
        onInstallUpdate={handleInstallUpdate}
        isRunningMapRef={isRunningMapRef}
        unreadIdsRef={unreadIdsRef}
        unreadCount={unreadCount}
        onMarkAllRead={markAllRead}
        imageBuildStatus={imageBuildStatus}
        imageUpdateAvailable={imageUpdateAvailable}
        onRebuildImage={handleRebuildImage}
        onToggleSidebar={() => setSidebarOpen((v) => !v)}
      />
      {selectedId && selectedChannel ? (
        <>
          <WorkspaceLayout
            ref={layoutRef}
            key={`layout-${selectedId}-${mountKey}`}
            channelId={selectedId}
            channel={selectedChannel}
            sidebarOpen={sidebarOpen}
            style={readmeOpen || settingsOpen || containersOpen || tasksOpen || workflowsOpen ? { display: "none" } : undefined}
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
            parentIsWorktree={!!(selectedChannel.parent_id && channels.find((c) => c.id === selectedChannel.parent_id)?.worktree)}
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
              channel={selectedChannel}
              maximized
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("readme")}
            />
          )}
          {settingsOpen && (
            <Settings
              open={settingsOpen}
              projectDirPath={settingsDirPath}
              channelId={settingsChannelId}
              channel={settingsChannel || selectedChannel}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("settings")}
              onDaemonRestarted={loadChannels}
              imageBuildStatus={imageBuildStatus}
              imageUpdateAvailable={imageUpdateAvailable}
              onRebuildImage={handleRebuildImage}
              onConfigDirtyChange={setConfigDirty}
            />
          )}
          {containersOpen && (
            <ContainersPanel
              ref={containersPanelRef}
              channel={selectedChannel}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("containers")}
            />
          )}
          {tasksOpen && (
            <GlobalTasksPanel
              channel={selectedChannel}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("tasks")}
              onSelectChannel={(id) => { closePanel("tasks"); handleSelect(id); }}
            />
          )}
          {workflowsOpen && (
            <WorkflowsGlobalPanel
              ref={workflowsPanelRef}
              channel={selectedChannel}
              sidebarOpen={sidebarOpen}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("workflows")}
            />
          )}
        </>
      ) : (
        <>
          {settingsOpen ? (
            <Settings
              open={settingsOpen}
              projectDirPath={settingsDirPath}
              channelId={settingsChannelId}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("settings")}
              onDaemonRestarted={loadChannels}
              imageBuildStatus={imageBuildStatus}
              imageUpdateAvailable={imageUpdateAvailable}
              onRebuildImage={handleRebuildImage}
              onConfigDirtyChange={setConfigDirty}
            />
          ) : containersOpen ? (
            <ContainersPanel
              ref={containersPanelRef}
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("containers")}
            />
          ) : tasksOpen ? (
            <GlobalTasksPanel
              sidebarOpen={sidebarOpen}
              onToggleSidebar={() => setSidebarOpen((v) => !v)}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("tasks")}
              onSelectChannel={(id) => { closePanel("tasks"); handleSelect(id); }}
            />
          ) : workflowsOpen ? (
            <WorkflowsGlobalPanel
              ref={workflowsPanelRef}
              sidebarOpen={sidebarOpen}
              onOpenPalette={() => setPaletteOpen(true)}
              onClose={() => closePanel("workflows")}
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

      {/* Unsaved config changes — confirm channel switch */}
      {pendingSelectId !== null && (
        <div style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
          onClick={() => setPendingSelectId(null)}>
          <div style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 360, boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
            onClick={(e) => e.stopPropagation()}>
            <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 8 }}>Unsaved Changes</div>
            <div style={{ fontSize: 13, color: colors.textDim, marginBottom: 16, lineHeight: 1.5 }}>
              You have unsaved config changes. Discard them and switch channels?
            </div>
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
              <button onClick={() => setPendingSelectId(null)} style={{
                padding: "6px 14px", backgroundColor: "transparent", border: `1px solid ${colors.border}`,
                borderRadius: 6, color: colors.text, fontSize: 12, cursor: "pointer", fontFamily: "inherit",
              }}>Cancel</button>
              <button onClick={() => { const id = pendingSelectId; setPendingSelectId(null); doSelect(id); }} style={{
                padding: "6px 14px", backgroundColor: colors.error, border: "none",
                borderRadius: 6, color: colors.white, fontSize: 12, fontWeight: 500, cursor: "pointer", fontFamily: "inherit",
              }}>Discard & Switch</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
