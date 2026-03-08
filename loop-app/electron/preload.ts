import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("loopAPI", {
  getApiUrl: (): Promise<string> => ipcRenderer.invoke("get-api-url"),
});
