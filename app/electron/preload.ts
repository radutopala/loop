import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("loopAPI", {
  getApiUrl: (): Promise<string> => ipcRenderer.invoke("get-api-url"),
  onNavigateChannel: (callback: (channelId: string) => void) => {
    ipcRenderer.on("navigate-channel", (_event, channelId: string) => {
      callback(channelId);
    });
  },
});
