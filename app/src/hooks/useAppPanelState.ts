import { useCallback, useState } from "react";

type PanelName = "settings" | "readme" | "containers" | "tasks" | "workflows" | "shares";

export interface AppPanelState {
  settingsOpen: boolean;
  readmeOpen: boolean;
  containersOpen: boolean;
  tasksOpen: boolean;
  workflowsOpen: boolean;
  sharesOpen: boolean;
  settingsDirPath: string | null;
  /** The Settings section to open on; null for its default. */
  settingsSection: string | null;
  configDirty: boolean;
  pendingSelectId: string | null;

  setConfigDirty: (dirty: boolean) => void;
  setPendingSelectId: (id: string | null) => void;

  /** Close all other panels and toggle the named one. */
  togglePanel: (panel: PanelName) => void;
  /** Open settings, optionally clearing the dir path. */
  openSettings: (dirPath?: string | null) => void;
  /**
   * Open settings for a specific dir path. If settings is already open with
   * the same dir path, close it instead (toggle behavior).
   */
  openConfig: (dirPath: string) => void;
  /** Open settings on a project's config, where its trust notice is. */
  openProjectTrust: (dirPath: string) => void;
  /**
   * Toggle settings without closing other panels first (keyboard shortcut
   * behavior: only close others when opening, not when closing).
   */
  toggleSettingsKeyboard: () => void;
  /**
   * Force settings open (no toggle) for the main-process menu item.
   * Clears dir path and closes all other panels.
   */
  forceOpenSettings: () => void;
  /** Close a single panel by name. */
  closePanel: (panel: PanelName) => void;
  /** Close all panels and reset configDirty. Used by doSelect. */
  closeAllPanels: () => void;
}

export function useAppPanelState(): AppPanelState {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [readmeOpen, setReadmeOpen] = useState(false);
  const [containersOpen, setContainersOpen] = useState(false);
  const [tasksOpen, setTasksOpen] = useState(false);
  const [workflowsOpen, setWorkflowsOpen] = useState(false);
  const [sharesOpen, setSharesOpen] = useState(false);
  const [settingsDirPath, setSettingsDirPath] = useState<string | null>(null);
  const [settingsSection, setSettingsSection] = useState<string | null>(null);
  const [configDirty, setConfigDirty] = useState(false);
  const [pendingSelectId, setPendingSelectId] = useState<string | null>(null);

  const togglePanel = useCallback((panel: PanelName) => {
    setSettingsOpen(panel === "settings" ? (v) => !v : false);
    setReadmeOpen(panel === "readme" ? (v) => !v : false);
    setContainersOpen(panel === "containers" ? (v) => !v : false);
    setTasksOpen(panel === "tasks" ? (v) => !v : false);
    setWorkflowsOpen(panel === "workflows" ? (v) => !v : false);
    setSharesOpen(panel === "shares" ? (v) => !v : false);
    if (panel === "settings") {
      setSettingsDirPath(null);
      setSettingsSection(null);
    }
  }, []);

  const openSettings = useCallback((dirPath?: string | null) => {
    setReadmeOpen(false);
    setContainersOpen(false);
    setTasksOpen(false);
    setWorkflowsOpen(false);
    setSharesOpen(false);
    setSettingsOpen((v) => !v);
    setSettingsDirPath(dirPath ?? null);
    setSettingsSection(null);
  }, []);

  const openConfig = useCallback(
    (dirPath: string) => {
      setReadmeOpen(false);
      setContainersOpen(false);
      setTasksOpen(false);
      setWorkflowsOpen(false);
      setSharesOpen(false);
      setSettingsOpen((v) => {
        if (v && settingsDirPath === dirPath) return false;
        setSettingsDirPath(dirPath);
        return true;
      });
      setSettingsSection(null);
    },
    [settingsDirPath],
  );

  const openProjectTrust = useCallback((dirPath: string) => {
    setReadmeOpen(false);
    setContainersOpen(false);
    setTasksOpen(false);
    setWorkflowsOpen(false);
    setSharesOpen(false);
    setSettingsOpen(true);
    setSettingsDirPath(dirPath);
    setSettingsSection("__project_json__");
  }, []);

  const toggleSettingsKeyboard = useCallback(() => {
    setSettingsOpen((v) => {
      if (!v) {
        setReadmeOpen(false);
        setContainersOpen(false);
        setTasksOpen(false);
        setWorkflowsOpen(false);
        setSharesOpen(false);
      }
      return !v;
    });
    setSettingsDirPath(null);
    setSettingsSection(null);
  }, []);

  const forceOpenSettings = useCallback(() => {
    setReadmeOpen(false);
    setContainersOpen(false);
    setTasksOpen(false);
    setWorkflowsOpen(false);
    setSharesOpen(false);
    setSettingsOpen(true);
    setSettingsDirPath(null);
    setSettingsSection(null);
  }, []);

  const closePanel = useCallback((panel: PanelName) => {
    switch (panel) {
      case "settings":
        setSettingsOpen(false);
        break;
      case "readme":
        setReadmeOpen(false);
        break;
      case "containers":
        setContainersOpen(false);
        break;
      case "tasks":
        setTasksOpen(false);
        break;
      case "workflows":
        setWorkflowsOpen(false);
        break;
      case "shares":
        setSharesOpen(false);
        break;
    }
  }, []);

  const closeAllPanels = useCallback(() => {
    setReadmeOpen(false);
    setSettingsOpen(false);
    setContainersOpen(false);
    setTasksOpen(false);
    setWorkflowsOpen(false);
    setSharesOpen(false);
    setConfigDirty(false);
  }, []);

  return {
    settingsOpen,
    readmeOpen,
    containersOpen,
    tasksOpen,
    workflowsOpen,
    sharesOpen,
    settingsDirPath,
    settingsSection,
    configDirty,
    pendingSelectId,
    setConfigDirty,
    setPendingSelectId,
    togglePanel,
    openSettings,
    openConfig,
    openProjectTrust,
    toggleSettingsKeyboard,
    forceOpenSettings,
    closePanel,
    closeAllPanels,
  };
}
