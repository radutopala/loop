import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, screen, session, shell, systemPreferences, Tray } from "electron";
import { autoUpdater } from "electron-updater";
import { startMainProcess } from "./mainProcess";

// The entry point: hands the real Electron, Node and process objects to the
// main process setup.
startMainProcess({
  electron: { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, screen, session, shell, systemPreferences, Tray },
  autoUpdater,
  fs,
  execFileSync,
  fetch: (input, init) => fetch(input, init),
  homedir: os.homedir,
  sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
  dirname: path.dirname(fileURLToPath(import.meta.url)),
  process,
});
