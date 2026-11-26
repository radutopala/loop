import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { MainProcessDeps } from "./mainProcess";

const startMainProcessMock = vi.fn();

const mockElectron = {
  app: { name: "app" },
  BrowserWindow: { name: "BrowserWindow" },
  dialog: { name: "dialog" },
  ipcMain: { name: "ipcMain" },
  Menu: { name: "Menu" },
  nativeImage: { name: "nativeImage" },
  screen: { name: "screen" },
  session: { name: "session" },
  shell: { name: "shell" },
  systemPreferences: { name: "systemPreferences" },
  Tray: { name: "Tray" },
};

const mockAutoUpdater = { checkForUpdates: vi.fn() };

vi.mock("electron", () => mockElectron);
vi.mock("electron-updater", () => ({ autoUpdater: mockAutoUpdater }));
vi.mock("./mainProcess", () => ({ startMainProcess: startMainProcessMock }));

describe("electron/main.ts", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("wires real dependencies into startMainProcess", async () => {
    const fetchMock = vi.fn(async () => new Response("ok"));
    vi.stubGlobal("fetch", fetchMock);

    await import("./main");

    expect(startMainProcessMock).toHaveBeenCalledTimes(1);
    const deps = startMainProcessMock.mock.calls[0]?.[0] as MainProcessDeps;
    expect(deps.electron).toEqual(mockElectron);
    expect(deps.autoUpdater).toBe(mockAutoUpdater);
    expect(deps.dirname).toBe(path.dirname(fileURLToPath(import.meta.url)));
    expect(deps.process).toBe(process);

    await deps.fetch("http://127.0.0.1:8222/api/health", { method: "GET" });
    expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:8222/api/health", { method: "GET" });

    await expect(deps.sleep(0)).resolves.toBeUndefined();
    expect(deps.homedir()).toBe(os.homedir());
    expect(deps.fs).toBe(fs);
    expect(deps.execFileSync).toBe(execFileSync);
  });
});
