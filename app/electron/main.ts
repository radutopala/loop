import { app, BrowserWindow, dialog, ipcMain, Menu, shell } from "electron";
import { autoUpdater } from "electron-updater";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

app.setName("Loop");

// --- ~/.loop directory & config ---

function loopDir(): string {
  return path.join(os.homedir(), ".loop");
}

function loopConfigPath(): string {
  return path.join(loopDir(), "config.json");
}

/** Strip HJSON comments (// and /* *​/) and trailing commas to get valid JSON. */
function stripHJSON(text: string): string {
  // Remove single-line comments (but not inside strings).
  // Simple heuristic: remove lines where // is not inside a quoted value.
  let result = "";
  let inString = false;
  let escape = false;
  let i = 0;
  while (i < text.length) {
    const ch = text[i]!;
    if (escape) {
      result += ch;
      escape = false;
      i++;
      continue;
    }
    if (ch === "\\") {
      escape = true;
      result += ch;
      i++;
      continue;
    }
    if (ch === '"') {
      inString = !inString;
      result += ch;
      i++;
      continue;
    }
    if (!inString) {
      if (ch === "/" && text[i + 1] === "/") {
        // Skip to end of line
        while (i < text.length && text[i] !== "\n") i++;
        continue;
      }
      if (ch === "/" && text[i + 1] === "*") {
        i += 2;
        while (i < text.length && !(text[i] === "*" && text[i + 1] === "/")) i++;
        i += 2; // skip */
        continue;
      }
    }
    result += ch;
    i++;
  }
  // Remove trailing commas before } or ]
  return result.replace(/,\s*([}\]])/g, "$1");
}

interface LoopConfig {
  api_addr?: string;
}

function readLoopConfig(): LoopConfig {
  try {
    const data = fs.readFileSync(loopConfigPath(), "utf-8");
    return JSON.parse(stripHJSON(data));
  } catch {
    return {};
  }
}

function hasLoopConfig(): boolean {
  return fs.existsSync(loopConfigPath());
}

/** Resolve API base URL from config's api_addr (e.g. ":8222" → "http://localhost:8222"). */
function resolveApiUrl(): string {
  if (process.env.LOOP_API_URL) return process.env.LOOP_API_URL;
  const cfg = readLoopConfig();
  const addr = cfg.api_addr || ":8222";
  const host = addr.startsWith(":") ? `localhost${addr}` : addr;
  return `http://${host}`;
}

// --- Desktop config helpers (read from ~/.loop/config.json desktop section) ---

function readDesktopConfig(): Record<string, unknown> {
  try {
    const data = fs.readFileSync(loopConfigPath(), "utf-8");
    const config = JSON.parse(stripHJSON(data));
    if (config.desktop && typeof config.desktop === "object") return config.desktop;
  } catch { /* ignore */ }
  return {};
}

// --- Bundled binary resolution ---

function binaryName(): string {
  return process.platform === "win32" ? "loop.exe" : "loop";
}

function bundledBinaryPath(): string | null {
  // In production: resources/bin/loop (or loop.exe on Windows)
  // process.resourcesPath points to <app>/Contents/Resources (macOS) or <app>/resources (Linux/Windows)
  const name = binaryName();
  const resourcePath = path.join(process.resourcesPath, "bin", name);
  if (fs.existsSync(resourcePath)) return resourcePath;
  // Fallback: old flat layout (resources/loop)
  const flatPath = path.join(process.resourcesPath, name);
  if (fs.existsSync(flatPath)) return flatPath;
  return null;
}

function findLoopBinary(): string | null {
  // 1. Bundled binary
  const bundled = bundledBinaryPath();
  if (bundled) return bundled;

  // 2. On PATH (for dev mode / system install)
  try {
    const whichCmd = process.platform === "win32" ? "where" : "which";
    const result = execFileSync(whichCmd, [binaryName()], { encoding: "utf-8" }).trim();
    // 'where' on Windows may return multiple lines — take the first.
    const firstLine = result.split(/\r?\n/)[0];
    if (firstLine) return firstLine;
  } catch {
    // not found on PATH
  }
  return null;
}

// --- Daemon management ---

async function isDaemonRunning(): Promise<boolean> {
  try {
    const resp = await fetch(`${resolveApiUrl()}/api/health`, {
      signal: AbortSignal.timeout(2000),
    });
    return resp.ok;
  } catch {
    return false;
  }
}

function ensureLoopConfig(): void {
  if (hasLoopConfig()) return;

  const binary = findLoopBinary();
  if (!binary) return;

  console.log("No ~/.loop/config.json found, running onboard:global");
  try {
    execFileSync(binary, ["onboard:global"], { encoding: "utf-8", timeout: 10_000 });
    console.log("Created ~/.loop/config.json");
  } catch (err) {
    console.warn("onboard:global failed:", err);
  }
}

/** Start or restart daemon via `loop daemon:restart` (installs as launchd/systemd service). */
async function ensureDaemon(): Promise<void> {
  ensureLoopConfig();

  // Daemon management is not supported on Windows yet — check if already running.
  if (process.platform === "win32") {
    if (await isDaemonRunning()) {
      console.log("Loop daemon is already running");
    } else {
      console.log("Loop daemon not running — start it manually with 'loop serve' on Windows");
    }
    return;
  }

  const binary = findLoopBinary();
  if (!binary) return;

  console.log(`Starting loop daemon via: ${binary} daemon:restart`);
  try {
    execFileSync(binary, ["daemon:restart"], { encoding: "utf-8", timeout: 30_000 });
  } catch (err) {
    console.warn("daemon:restart failed:", err);
  }

  // Wait for daemon to become healthy
  for (let i = 0; i < 30; i++) {
    await new Promise((r) => setTimeout(r, 500));
    if (await isDaemonRunning()) {
      console.log("Loop daemon is ready");
      return;
    }
  }
  console.warn("Loop daemon did not become healthy within 15s");
}

// installCLI creates a symlink so `loop` is available on the user's PATH.
// When interactive is true, shows a dialog with the result.
function installCLI(interactive = false): void {
  const bundled = bundledBinaryPath();
  if (!bundled) {
    if (interactive) dialog.showMessageBox({ message: "CLI binary not found.", detail: "Not available in dev mode.", buttons: ["OK"] });
    return;
  }

  const linkPath = process.platform === "win32"
    ? path.join(process.env.LOCALAPPDATA || "", "Loop", "bin", "loop.cmd")
    : "/usr/local/bin/loop";

  // Check if already installed correctly.
  try {
    if (process.platform === "win32") {
      const expected = `@echo off\n"${bundled}" %*\n`;
      if (fs.existsSync(linkPath) && fs.readFileSync(linkPath, "utf-8") === expected) {
        if (interactive) dialog.showMessageBox({ message: "CLI already installed.", detail: linkPath, buttons: ["OK"] });
        return;
      }
    } else {
      if (fs.readlinkSync(linkPath) === bundled) {
        if (interactive) dialog.showMessageBox({ message: "CLI already installed.", detail: linkPath, buttons: ["OK"] });
        return;
      }
    }
  } catch { /* not installed yet */ }

  try {
    if (process.platform === "win32") {
      const binDir = path.dirname(linkPath);
      fs.mkdirSync(binDir, { recursive: true });
      fs.writeFileSync(linkPath, `@echo off\n"${bundled}" %*\n`);
    } else {
      // Try direct symlink first.
      try { fs.unlinkSync(linkPath); } catch { /* ignore */ }
      fs.symlinkSync(bundled, linkPath);
    }
    if (interactive) dialog.showMessageBox({ message: "CLI installed.", detail: `${linkPath} -> ${bundled}`, buttons: ["OK"] });
  } catch {
    // Needs elevated permissions.
    try {
      if (process.platform === "darwin") {
        execFileSync("osascript", ["-e", `do shell script "ln -sf '${bundled}' '${linkPath}'" with administrator privileges`], { timeout: 60_000 });
      } else if (process.platform !== "win32") {
        execFileSync("pkexec", ["ln", "-sf", bundled, linkPath], { timeout: 60_000 });
      }
      if (interactive) dialog.showMessageBox({ message: "CLI installed.", detail: `${linkPath} -> ${bundled}`, buttons: ["OK"] });
    } catch {
      if (interactive) dialog.showMessageBox({ type: "error", message: "Could not install CLI.", detail: `Run manually:\nsudo ln -sf "${bundled}" ${linkPath}`, buttons: ["OK"] });
    }
  }
}

const PROTOCOL = "loop";

if (process.defaultApp) {
  // In dev mode, register with the path to the electron binary + script.
  if (process.argv.length >= 2) {
    app.setAsDefaultProtocolClient(PROTOCOL, process.execPath, [
      path.resolve(process.argv[1]!),
    ]);
  }
} else {
  app.setAsDefaultProtocolClient(PROTOCOL);
}

// Ensure single instance — second instance passes its URL to the first.
const gotTheLock = app.requestSingleInstanceLock();
if (!gotTheLock) {
  app.quit();
}

// Use the macOS-specific icon for the dock (has rounded-rect background).
// The original loop.png is kept for internal UI use (favicon, panels).
const iconMacos = process.env.VITE_DEV_SERVER_URL
  ? path.join(__dirname, "../public/loop-macos.png")
  : path.join(__dirname, "../dist/loop-macos.png");
if (process.platform === "darwin") {
  app.dock?.setIcon(iconMacos);
}

const VITE_DEV_SERVER_URL = process.env.VITE_DEV_SERVER_URL;

function parseChannelId(url: string): string {
  // URL format: loop://channel/<channel-id>
  try {
    const parsed = new URL(url);
    return parsed.pathname.replace(/^\/+/, "");
  } catch {
    return "";
  }
}

function initialBackgroundColor(): string {
  const theme = (readDesktopConfig().theme as string) ?? "dark";
  if (theme === "claude") return "#FAF6F1";
  if (theme === "light") return "#ffffff";
  return "#212121"; // dark
}

function createWindow(hash?: string): BrowserWindow {
  const preloadPath = path.join(__dirname, "preload.cjs");
  const win = new BrowserWindow({
    title: "Loop",
    icon: iconMacos, // undefined in production — uses .icns from electron-builder
    width: 1200,
    height: 800,
    minWidth: 900,
    minHeight: 400,
    backgroundColor: initialBackgroundColor(),
    titleBarStyle: process.platform === "darwin" ? "hiddenInset" : "hidden",
    webPreferences: {
      preload: preloadPath,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  win.maximize();

  const fragment = hash ? `#${hash}` : "";

  if (VITE_DEV_SERVER_URL) {
    win.loadURL(`${VITE_DEV_SERVER_URL}${fragment}`);
  } else {
    win.loadFile(path.join(__dirname, "../dist/index.html"), {
      hash: hash || undefined,
    });
  }

  // Handle protocol URLs that arrived while the page was loading.
  win.webContents.on("did-finish-load", () => {
    if (pendingChannelId) {
      navigateToChannel(pendingChannelId);
      pendingChannelId = null;
    }
  });

  // Open external links (http/https) in the native browser instead of Electron.
  // Also deny `about:blank` popups so that any caller doing `window.open()` with
  // no URL (e.g. the noopener trick) does not spawn an in-app Loop window.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (url.startsWith("http://") || url.startsWith("https://")) {
      shell.openExternal(url);
      return { action: "deny" };
    }
    if (url === "" || url === "about:blank") {
      return { action: "deny" };
    }
    return { action: "allow" };
  });

  return win;
}

/** Returns the most recently focused window, or the first available one. */
function getFocusedOrLastWindow(): BrowserWindow | null {
  return (
    BrowserWindow.getFocusedWindow() ??
    BrowserWindow.getAllWindows()[0] ??
    null
  );
}

function navigateToChannel(channelId: string) {
  const win = getFocusedOrLastWindow();
  if (!channelId || !win) return;
  // Set hash directly on the page — triggers the renderer's hashchange listener.
  // This avoids IPC timing issues where the listener isn't mounted yet.
  win.webContents.executeJavaScript(
    `window.location.hash = ${JSON.stringify(channelId)}`,
  );
  if (win.isMinimized()) win.restore();
  win.focus();
}

let pendingChannelId: string | null = null;

// macOS: open-url fires when app is already running or being launched.
// It can fire before OR after the ready event.
app.on("open-url", (event, url) => {
  event.preventDefault();
  const channelId = parseChannelId(url);
  if (!channelId) return;
  const win = getFocusedOrLastWindow();
  if (win && !win.webContents.isLoading()) {
    navigateToChannel(channelId);
  } else {
    // Window not ready yet — store for createWindow hash or did-finish-load.
    pendingChannelId = channelId;
  }
});

// Windows/Linux: second instance passes argv with the URL.
app.on("second-instance", (_event, argv) => {
  const url = argv.find((a) => a.startsWith(`${PROTOCOL}://`));
  if (url) {
    const channelId = parseChannelId(url);
    if (channelId) navigateToChannel(channelId);
  }
  const win = getFocusedOrLastWindow();
  if (win) {
    if (win.isMinimized()) win.restore();
    win.focus();
  }
});

function buildMenu() {
  const isMac = process.platform === "darwin";
  const template: Electron.MenuItemConstructorOptions[] = [
    ...(isMac
      ? [
          {
            label: app.name,
            submenu: [
              { role: "about" as const },
              {
                label: "Check for Updates…",
                click: async () => {
                  if (VITE_DEV_SERVER_URL) {
                    dialog.showMessageBox({ message: "Updates are not available in dev mode.", buttons: ["OK"] });
                    return;
                  }
                  try {
                    const result = await autoUpdater.checkForUpdates();
                    if (!result || !result.updateInfo || result.updateInfo.version === app.getVersion()) {
                      dialog.showMessageBox({ message: "You're up to date!", detail: `Loop ${app.getVersion()} is the latest version.`, buttons: ["OK"] });
                    }
                  } catch {
                    dialog.showMessageBox({ message: "Unable to check for updates.", detail: "Please try again later.", buttons: ["OK"] });
                  }
                },
              },
              {
                label: "Install CLI",
                click: () => installCLI(true),
              },
              { type: "separator" as const },
              {
                label: "Settings…",
                accelerator: "CmdOrCtrl+," as const,
                click: () => {
                  const win = getFocusedOrLastWindow();
                  if (win) {
                    win.webContents.send("open-settings");
                  }
                },
              },
              { type: "separator" as const },
              { role: "services" as const },
              { type: "separator" as const },
              { role: "hide" as const },
              { role: "hideOthers" as const },
              { role: "unhide" as const },
              { type: "separator" as const },
              { role: "quit" as const },
            ],
          },
        ]
      : []),
    {
      label: "File",
      submenu: [
        {
          label: "New Window",
          accelerator: "CmdOrCtrl+N",
          click: async () => {
            const focused = BrowserWindow.getFocusedWindow();
            let hash: string | undefined;
            if (focused) {
              try {
                const h = await focused.webContents.executeJavaScript("window.location.hash.slice(1)");
                if (h) hash = h;
              } catch { /* ignore */ }
            }
            createWindow(hash);
          },
        },
        { type: "separator" },
        isMac ? { role: "close" } : { role: "quit" },
      ],
    },
    {
      label: "Edit",
      submenu: [
        { role: "undo" },
        { role: "redo" },
        { type: "separator" },
        { role: "cut" },
        { role: "copy" },
        { role: "paste" },
        { role: "selectAll" },
      ],
    },
    {
      label: "View",
      submenu: [
        { role: "reload" },
        { role: "forceReload" },
        { role: "toggleDevTools" },
        { type: "separator" },
        { role: "resetZoom" },
        { role: "zoomIn" },
        { role: "zoomOut" },
        { type: "separator" },
        { role: "togglefullscreen" },
      ],
    },
    {
      role: "windowMenu",
    },
  ];

  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

// --- Auto-updater ---

let updateStatus: { available: boolean; version?: string; downloading: boolean; downloaded: boolean; error?: string } = {
  available: false, downloading: false, downloaded: false,
};

function setupAutoUpdater() {
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = false;

  autoUpdater.on("update-available", (info) => {
    updateStatus = { available: true, version: info.version, downloading: false, downloaded: false };
    for (const win of BrowserWindow.getAllWindows()) {
      win.webContents.send("update-status", updateStatus);
    }
  });

  autoUpdater.on("update-not-available", () => {
    updateStatus = { available: false, downloading: false, downloaded: false };
  });

  autoUpdater.on("download-progress", () => {
    updateStatus = { ...updateStatus, downloading: true };
    for (const win of BrowserWindow.getAllWindows()) {
      win.webContents.send("update-status", updateStatus);
    }
  });

  autoUpdater.on("update-downloaded", () => {
    updateStatus = { ...updateStatus, downloading: false, downloaded: true };
    for (const win of BrowserWindow.getAllWindows()) {
      win.webContents.send("update-status", updateStatus);
    }
  });

  autoUpdater.on("error", (err) => {
    updateStatus = { ...updateStatus, downloading: false, error: String(err) };
    console.warn("Auto-updater error:", err);
    for (const win of BrowserWindow.getAllWindows()) {
      win.webContents.send("update-status", updateStatus);
    }
  });

  autoUpdater.checkForUpdates().catch(() => {});
  setInterval(() => {
    autoUpdater.checkForUpdates().catch(() => {});
  }, 30 * 60 * 1000);
}

app.on("ready", async () => {
  buildMenu();
  if (!VITE_DEV_SERVER_URL) {
    await ensureDaemon();
  }
  createWindow(pendingChannelId || undefined);
  pendingChannelId = null;

  if (!VITE_DEV_SERVER_URL) {
    setupAutoUpdater();
  }
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("before-quit", () => {
  const desktop = readDesktopConfig();
  if (desktop.stop_daemon_on_quit) {
    const binary = findLoopBinary();
    if (binary) {
      console.log(`Stopping loop daemon via: ${binary} daemon:stop`);
      try {
        execFileSync(binary, ["daemon:stop"], { encoding: "utf-8", timeout: 15_000 });
      } catch (err) {
        console.warn("daemon:stop failed:", err);
      }
    }
  }
});

app.on("activate", () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
});

// Debounce turn-ended bounces: while the window is unfocused, only bounce on
// the first turn-end and ignore subsequent ones until the user focuses Loop
// again. Without this, a chain of turns (agent back-and-forth, multiple
// scheduled completions) bounces the dock repeatedly even though one nudge
// is plenty.
let turnEndBouncedSinceFocus = false;

ipcMain.on("turn-ended", () => {
  const wins = BrowserWindow.getAllWindows();
  if (wins.some((w) => w.isFocused())) return;
  if (turnEndBouncedSinceFocus) return;
  turnEndBouncedSinceFocus = true;
  if (process.platform === "darwin") {
    app.dock?.bounce("informational");
  } else {
    for (const w of wins) w.flashFrame(true);
  }
});

// Pending approval requests, keyed by req_id. While this set is non-empty and
// no Loop window is focused, re-fire bounce("critical") on a timer — recent
// macOS versions ignore the "until activated" promise and stop after a single
// bounce, so we drive it ourselves to keep the dock bumping.
const pendingApprovals = new Set<string>();
let approvalBounceId: number | null = null;
let approvalBounceInterval: NodeJS.Timeout | null = null;
const APPROVAL_BOUNCE_INTERVAL_MS = 2000;

function pendingSnapshot(): string {
  return `[${[...pendingApprovals].join(",")}]`;
}

function startApprovalBounce() {
  if (pendingApprovals.size === 0) return;
  const wins = BrowserWindow.getAllWindows();
  if (wins.some((w) => w.isFocused())) return;

  if (process.platform !== "darwin") {
    for (const w of wins) w.flashFrame(true);
    return;
  }

  if (approvalBounceInterval) return;
  console.log(`[bounce] starting approval bounce loop size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
  let tickCount = 0;
  const tick = () => {
    if (pendingApprovals.size === 0) return;
    if (BrowserWindow.getAllWindows().some((w) => w.isFocused())) return;
    tickCount++;
    if (tickCount === 1 || tickCount % 10 === 0) {
      console.log(`[bounce] tick=${tickCount} size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
    }
    approvalBounceId = app.dock?.bounce("critical") ?? null;
  };
  tick();
  approvalBounceInterval = setInterval(tick, APPROVAL_BOUNCE_INTERVAL_MS);
}

function stopApprovalBounce() {
  if (approvalBounceInterval) {
    console.log(`[bounce] stopping approval bounce loop size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
    clearInterval(approvalBounceInterval);
    approvalBounceInterval = null;
  }
  if (process.platform === "darwin") {
    if (approvalBounceId !== null) {
      app.dock?.cancelBounce(approvalBounceId);
      approvalBounceId = null;
    }
  } else {
    for (const w of BrowserWindow.getAllWindows()) w.flashFrame(false);
  }
}

ipcMain.on("approval-needed", (_event, reqId?: string) => {
  const had = reqId ? pendingApprovals.has(reqId) : false;
  if (reqId) pendingApprovals.add(reqId);
  console.log(`[bounce] approval-needed reqId=${reqId ?? "<none>"} duplicate=${had} size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
  startApprovalBounce();
});

ipcMain.on("approval-resolved", (_event, reqId?: string) => {
  const had = reqId ? pendingApprovals.has(reqId) : false;
  if (reqId) pendingApprovals.delete(reqId);
  console.log(`[bounce] approval-resolved reqId=${reqId ?? "<none>"} known=${had} size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
  if (pendingApprovals.size === 0) stopApprovalBounce();
});

// reconcile-approvals replaces the bouncer's pending set with the canonical
// list reported by the renderer (which just snapshotted GET /api/gate/approvals).
// Used after a WS reconnect / page reload to drop orphaned req_ids whose
// resolve broadcast we missed — the symptom that wedged the dock-bouncer
// on bd446d95 even after the gate denied on timeout.
ipcMain.on("reconcile-approvals", (_event, reqIds?: string[]) => {
  const incoming = new Set<string>(Array.isArray(reqIds) ? reqIds : []);
  const dropped: string[] = [];
  for (const id of pendingApprovals) {
    if (!incoming.has(id)) {
      pendingApprovals.delete(id);
      dropped.push(id);
    }
  }
  for (const id of incoming) pendingApprovals.add(id);
  console.log(`[bounce] reconcile-approvals incoming=${incoming.size} dropped=${dropped.length} size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
  if (pendingApprovals.size === 0) {
    stopApprovalBounce();
  } else {
    startApprovalBounce();
  }
});

app.on("browser-window-focus", () => {
  if (approvalBounceInterval) {
    console.log(`[bounce] window focused, clearing interval (pending size=${pendingApprovals.size} pending=${pendingSnapshot()})`);
    clearInterval(approvalBounceInterval);
    approvalBounceInterval = null;
  }
  approvalBounceId = null;
  turnEndBouncedSinceFocus = false;
});

app.on("browser-window-blur", () => {
  setTimeout(() => {
    if (BrowserWindow.getAllWindows().some((w) => w.isFocused())) return;
    if (pendingApprovals.size > 0) {
      console.log(`[bounce] window blurred, pending size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
    }
    startApprovalBounce();
  }, 50);
});

ipcMain.on("set-theme", (_event, themeName: string) => {
  for (const win of BrowserWindow.getAllWindows()) {
    win.webContents.send("theme-changed", themeName);
  }
});

// Open an http(s) URL in the OS default browser. Called from the renderer
// for terminal link clicks so we never go through window.open (which the
// xterm WebLinksAddon uses by default and which spawns an extra Electron
// popup window before navigating).
ipcMain.handle("open-external", async (_event, url: string) => {
  if (typeof url !== "string") return;
  if (!url.startsWith("http://") && !url.startsWith("https://")) return;
  await shell.openExternal(url);
});

ipcMain.handle("get-update-status", () => updateStatus);

ipcMain.handle("download-update", async () => {
  await autoUpdater.downloadUpdate();
});

ipcMain.handle("install-update", async () => {
  // Remove Docker image so daemon rebuilds with new binary.
  try {
    const url = resolveApiUrl();
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 30_000);
    await fetch(`${url}/api/image`, { method: "DELETE", signal: controller.signal });
    clearTimeout(timeout);
  } catch {
    // Best-effort — image will be stale but functional
  }

  // Restart daemon so it picks up the new bundled binary.
  const binary = findLoopBinary();
  if (binary) {
    try {
      execFileSync(binary, ["daemon:restart"], { encoding: "utf-8", timeout: 30_000 });
    } catch (err) {
      console.warn("daemon:restart before update failed:", err);
    }
  }
  autoUpdater.quitAndInstall(false, true);
});

ipcMain.handle("show-open-directory-dialog", async () => {
  const result = await dialog.showOpenDialog({
    properties: ["openDirectory"],
    title: "Select project directory",
  });
  if (result.canceled || result.filePaths.length === 0) return null;
  return result.filePaths[0];
});

ipcMain.handle("onboard-local", async (_event, dirPath: string) => {
  const binary = findLoopBinary();
  if (!binary) return { ok: false, error: "loop binary not found" };
  try {
    const output = execFileSync(binary, ["onboard:local", "--api-url", resolveApiUrl(), "--platform", "local"], {
      cwd: dirPath,
      encoding: "utf-8",
      timeout: 15_000,
    });
    return { ok: true, output };
  } catch (err) {
    return { ok: false, error: String(err) };
  }
});

ipcMain.handle("get-api-url", () => {
  return resolveApiUrl();
});

ipcMain.handle("get-daemon-info", async () => {
  return {
    running: await isDaemonRunning(),
    binaryPath: findLoopBinary(),
  };
});

ipcMain.handle("restart-daemon", async () => {
  const binary = findLoopBinary();
  if (binary) {
    try {
      execFileSync(binary, ["daemon:restart"], { encoding: "utf-8", timeout: 30_000 });
    } catch (err) {
      console.warn("daemon:restart failed:", err);
    }
  }

  // Wait for daemon to become healthy.
  for (let i = 0; i < 30; i++) {
    await new Promise((r) => setTimeout(r, 500));
    if (await isDaemonRunning()) break;
  }

  return {
    running: await isDaemonRunning(),
    binaryPath: findLoopBinary(),
  };
});
