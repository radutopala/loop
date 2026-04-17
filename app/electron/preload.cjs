const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("loopAPI", {
  getApiUrl: () => ipcRenderer.invoke("get-api-url"),
  onNavigateChannel: (callback) => {
    ipcRenderer.on("navigate-channel", (_event, channelId) => {
      callback(channelId);
    });
  },
  showOpenDirectoryDialog: () => ipcRenderer.invoke("show-open-directory-dialog"),
  onboardLocal: (dirPath) => ipcRenderer.invoke("onboard-local", dirPath),
  getDaemonInfo: () => ipcRenderer.invoke("get-daemon-info"),
  restartDaemon: () => ipcRenderer.invoke("restart-daemon"),
  onOpenSettings: (callback) => {
    ipcRenderer.on("open-settings", () => callback());
  },
  getUpdateStatus: () => ipcRenderer.invoke("get-update-status"),
  downloadUpdate: () => ipcRenderer.invoke("download-update"),
  installUpdate: () => ipcRenderer.invoke("install-update"),
  onUpdateStatus: (callback) => {
    ipcRenderer.on("update-status", (_event, status) => callback(status));
  },
  notifyTurnEnd: () => ipcRenderer.send("turn-ended"),
});
