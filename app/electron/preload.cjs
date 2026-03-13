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
  getSettings: () => ipcRenderer.invoke("get-settings"),
  saveSettings: (settings) => ipcRenderer.invoke("save-settings", settings),
  getDaemonInfo: () => ipcRenderer.invoke("get-daemon-info"),
  getConfig: () => ipcRenderer.invoke("get-config"),
  getProjectConfig: (dirPath) => ipcRenderer.invoke("get-project-config", dirPath),
  saveConfig: (filePath, content) => ipcRenderer.invoke("save-config", filePath, content),
  restartDaemon: () => ipcRenderer.invoke("restart-daemon"),
  onOpenSettings: (callback) => {
    ipcRenderer.on("open-settings", () => callback());
  },
});
