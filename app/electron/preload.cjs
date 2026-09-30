const { contextBridge, ipcRenderer } = require("electron");

// Subscribes to a main-process message and returns the unsubscribe, for the
// effect that subscribed to call on cleanup: without it, each effect re-run
// (StrictMode, Fast Refresh, changed deps) stacks another listener.
function subscribe(channel, handler) {
  ipcRenderer.on(channel, handler);
  return () => ipcRenderer.removeListener(channel, handler);
}

contextBridge.exposeInMainWorld("loopAPI", {
  getApiUrl: () => ipcRenderer.invoke("get-api-url"),
  getApiToken: () => ipcRenderer.invoke("get-api-token"),
  onNavigateChannel: (callback) => subscribe("navigate-channel", (_event, channelId) => callback(channelId)),
  showOpenDirectoryDialog: () => ipcRenderer.invoke("show-open-directory-dialog"),
  onboardLocal: (dirPath) => ipcRenderer.invoke("onboard-local", dirPath),
  getDaemonInfo: () => ipcRenderer.invoke("get-daemon-info"),
  restartDaemon: () => ipcRenderer.invoke("restart-daemon"),
  onOpenSettings: (callback) => subscribe("open-settings", () => callback()),
  getUpdateStatus: () => ipcRenderer.invoke("get-update-status"),
  downloadUpdate: () => ipcRenderer.invoke("download-update"),
  installUpdate: () => ipcRenderer.invoke("install-update"),
  onUpdateStatus: (callback) => subscribe("update-status", (_event, status) => callback(status)),
  notifyTurnEnd: () => ipcRenderer.send("turn-ended"),
  notifyApprovalNeeded: (reqId) => ipcRenderer.send("approval-needed", reqId),
  notifyApprovalResolved: (reqId) => ipcRenderer.send("approval-resolved", reqId),
  reconcileApprovals: (reqIds) => ipcRenderer.send("reconcile-approvals", reqIds),
  setTheme: (name) => ipcRenderer.send("set-theme", name),
  openExternal: (url) => ipcRenderer.invoke("open-external", url),
  onThemeChanged: (callback) => subscribe("theme-changed", (_event, name) => callback(name)),
});
