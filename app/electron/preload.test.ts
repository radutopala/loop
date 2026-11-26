import { readFileSync } from "node:fs";
import Module from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";
import { describe, expect, it, vi } from "vitest";

type Handler = (...args: unknown[]) => void;

describe("electron/preload.cjs", () => {
  it("exposes loopAPI over contextBridge and wires every IPC method and subscription", async () => {
    const exposed: Record<string, Record<string, (...args: unknown[]) => unknown>> = {};
    const listeners = new Map<string, Handler[]>();

    const fakeElectron = {
      contextBridge: {
        exposeInMainWorld: vi.fn((key: string, api: Record<string, (...args: unknown[]) => unknown>) => {
          exposed[key] = api;
        }),
      },
      ipcRenderer: {
        invoke: vi.fn(async (channel: string, ...args: unknown[]) => ({ channel, args })),
        send: vi.fn(),
        on: vi.fn((channel: string, handler: Handler) => {
          listeners.set(channel, [...(listeners.get(channel) ?? []), handler]);
        }),
        removeListener: vi.fn((channel: string, handler: Handler) => {
          listeners.set(
            channel,
            (listeners.get(channel) ?? []).filter((h) => h !== handler),
          );
        }),
      },
    };

    // Run the preload as Node would, with a require that hands it the fake
    // electron module.
    const file = fileURLToPath(new URL("./preload.cjs", import.meta.url));
    const wrapper = vm.runInThisContext(Module.wrap(readFileSync(file, "utf-8")), { filename: file }) as (...args: unknown[]) => void;
    const requireFake = (request: string) => {
      if (request !== "electron") throw new Error(`unexpected require: ${request}`);
      return fakeElectron;
    };
    const module = { exports: {} };
    wrapper(module.exports, requireFake, module, file, path.dirname(file));

    expect(fakeElectron.contextBridge.exposeInMainWorld).toHaveBeenCalledWith("loopAPI", expect.any(Object));
    const api = exposed.loopAPI;
    expect(api).toBeDefined();
    if (!api) return;

    // invoke methods
    await api.getApiUrl?.();
    await api.getApiToken?.();
    await api.showOpenDirectoryDialog?.();
    await api.onboardLocal?.("/repo");
    await api.getDaemonInfo?.();
    await api.restartDaemon?.();
    await api.getUpdateStatus?.();
    await api.downloadUpdate?.();
    await api.installUpdate?.();
    await api.openExternal?.("https://example.com");
    await api.microphoneAccess?.(1);
    await api.microphoneAccess?.(0);

    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("get-api-url");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("get-api-token");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("show-open-directory-dialog");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("onboard-local", "/repo");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("get-daemon-info");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("restart-daemon");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("get-update-status");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("download-update");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("install-update");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("open-external", "https://example.com");
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("microphone-access", true);
    expect(fakeElectron.ipcRenderer.invoke).toHaveBeenCalledWith("microphone-access", false);

    // send methods
    api.openChannel?.("ch-1");
    api.notifyTurnEnd?.();
    api.notifyApprovalNeeded?.("req-1");
    api.notifyApprovalResolved?.("req-1");
    api.reconcileApprovals?.(["req-1"]);
    api.setTheme?.("dark");
    api.inboxReport?.({ count: 2 });
    api.setInboxPinned?.(true);
    api.setInboxPinned?.(0);
    api.openChannelFromInbox?.("ch-2");
    api.moveInbox?.(10, 20);
    api.closeInbox?.();

    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("open-channel", "ch-1");
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("turn-ended");
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("approval-needed", "req-1");
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("approval-resolved", "req-1");
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("reconcile-approvals", ["req-1"]);
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("set-theme", "dark");
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("inbox-report", { count: 2 });
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("inbox-set-pinned", true);
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("inbox-set-pinned", false);
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("inbox-open-channel", "ch-2");
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("inbox-move", { x: 10, y: 20 });
    expect(fakeElectron.ipcRenderer.send).toHaveBeenCalledWith("inbox-close");

    // subscriptions
    const emit = (channel: string, ...args: unknown[]) => {
      for (const h of listeners.get(channel) ?? []) h({}, ...args);
    };

    const navCb = vi.fn();
    const unsubNav = api.onNavigateChannel?.(navCb) as () => void;
    emit("navigate-channel", "ch-9");
    expect(navCb).toHaveBeenCalledWith("ch-9");
    unsubNav();
    expect(listeners.get("navigate-channel")).toHaveLength(0);

    const settingsCb = vi.fn();
    const unsubSettings = api.onOpenSettings?.(settingsCb) as () => void;
    emit("open-settings");
    expect(settingsCb).toHaveBeenCalledTimes(1);
    unsubSettings();
    expect(listeners.get("open-settings")).toHaveLength(0);

    const updateCb = vi.fn();
    const unsubUpdate = api.onUpdateStatus?.(updateCb) as () => void;
    emit("update-status", { state: "ready" });
    expect(updateCb).toHaveBeenCalledWith({ state: "ready" });
    unsubUpdate();
    expect(listeners.get("update-status")).toHaveLength(0);

    const themeCb = vi.fn();
    const unsubTheme = api.onThemeChanged?.(themeCb) as () => void;
    emit("theme-changed", "light");
    expect(themeCb).toHaveBeenCalledWith("light");
    unsubTheme();
    expect(listeners.get("theme-changed")).toHaveLength(0);

    const inboxCb = vi.fn();
    const unsubInbox = api.onInboxShown?.(inboxCb) as () => void;
    emit("inbox-shown", 1);
    emit("inbox-shown", 0);
    expect(inboxCb).toHaveBeenNthCalledWith(1, true);
    expect(inboxCb).toHaveBeenNthCalledWith(2, false);
    unsubInbox();
    expect(listeners.get("inbox-shown")).toHaveLength(0);
  });
});
