import { app, BrowserWindow, dialog, ipcMain, Menu } from "electron";
import { execFileSync, spawn, ChildProcess } from "node:child_process";
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

// --- App settings (stored in ~/.loop/app.json) ---

interface Settings {
  stopDaemonOnQuit: boolean;
  autoSaveOnBlur: boolean;
}

const defaultSettings: Settings = {
  stopDaemonOnQuit: false,
  autoSaveOnBlur: true,
};

function appSettingsPath(): string {
  return path.join(loopDir(), "app.json");
}

function loadSettings(): Settings {
  try {
    const data = fs.readFileSync(appSettingsPath(), "utf-8");
    return { ...defaultSettings, ...JSON.parse(data) };
  } catch {
    return { ...defaultSettings };
  }
}

function saveSettings(settings: Settings): void {
  fs.mkdirSync(loopDir(), { recursive: true });
  fs.writeFileSync(appSettingsPath(), JSON.stringify(settings, null, 2));
}

// --- Bundled binary resolution ---

function bundledBinaryPath(): string | null {
  // In production: resources/bin/loop
  // process.resourcesPath points to <app>/Contents/Resources (macOS) or <app>/resources (Linux)
  const resourcePath = path.join(process.resourcesPath, "bin", "loop");
  if (fs.existsSync(resourcePath)) return resourcePath;
  // Fallback: old flat layout (resources/loop)
  const flatPath = path.join(process.resourcesPath, "loop");
  if (fs.existsSync(flatPath)) return flatPath;
  return null;
}

function findLoopBinary(): string | null {
  // 1. Bundled binary
  const bundled = bundledBinaryPath();
  if (bundled) return bundled;

  // 2. On PATH (for dev mode / system install)
  try {
    const result = execFileSync("which", ["loop"], { encoding: "utf-8" }).trim();
    if (result) return result;
  } catch {
    // not found on PATH
  }
  return null;
}

// --- Daemon management ---

let daemonProcess: ChildProcess | null = null;
let managedDaemon = false; // true if we started the daemon ourselves

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

async function ensureDaemon(): Promise<void> {
  ensureLoopConfig();

  if (await isDaemonRunning()) return;

  const binary = findLoopBinary();
  if (!binary) return; // no binary available — user must start daemon manually

  console.log(`Starting loop daemon from: ${binary}`);
  daemonProcess = spawn(binary, ["serve"], {
    stdio: "ignore",
    detached: true,
  });
  daemonProcess.unref();
  managedDaemon = true;

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

async function stopDaemon(): Promise<void> {
  if (!managedDaemon || !daemonProcess) return;
  console.log("Stopping loop daemon");
  daemonProcess.kill("SIGTERM");
  daemonProcess = null;
  managedDaemon = false;
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

function createWindow(hash?: string): BrowserWindow {
  const preloadPath = path.join(__dirname, "preload.cjs");
  const win = new BrowserWindow({
    title: "Loop",
    icon: iconMacos, // undefined in production — uses .icns from electron-builder
    width: 1200,
    height: 800,
    minWidth: 900,
    minHeight: 400,
    titleBarStyle: "hiddenInset",
    webPreferences: {
      preload: preloadPath,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

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
          click: () => createWindow(),
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

app.on("ready", async () => {
  buildMenu();
  await ensureDaemon();
  createWindow(pendingChannelId || undefined);
  pendingChannelId = null;
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("before-quit", async () => {
  const settings = loadSettings();
  if (settings.stopDaemonOnQuit) {
    await stopDaemon();
  }
});

app.on("activate", () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
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

ipcMain.handle("get-settings", () => {
  return loadSettings();
});

ipcMain.handle("save-settings", (_event, settings: Settings) => {
  saveSettings(settings);
  return true;
});

ipcMain.handle("get-daemon-info", async () => {
  return {
    running: await isDaemonRunning(),
    managed: managedDaemon,
    binaryPath: findLoopBinary(),
  };
});

ipcMain.handle("get-config", () => {
  try {
    return {
      path: loopConfigPath(),
      content: fs.readFileSync(loopConfigPath(), "utf-8"),
    };
  } catch {
    return { path: loopConfigPath(), content: null };
  }
});

ipcMain.handle("get-project-config", (_event, dirPath: string) => {
  const p = path.join(dirPath, ".loop", "config.json");
  try {
    return {
      path: p,
      content: fs.readFileSync(p, "utf-8"),
    };
  } catch {
    return { path: p, content: null };
  }
});

ipcMain.handle("save-config", (_event, filePath: string, content: string) => {
  try {
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, content);
    return { ok: true };
  } catch (err) {
    return { ok: false, error: String(err) };
  }
});

ipcMain.handle("restart-daemon", async () => {
  const cfg = readLoopConfig();
  const addr = cfg.api_addr || ":8222";
  const port = addr.includes(":") ? addr.split(":").pop() : "8222";

  /** Find PIDs listening on the daemon port. */
  function findListeningPids(): number[] {
    try {
      const out = execFileSync("lsof", ["-ti", `TCP:${port}`, "-sTCP:LISTEN"], { encoding: "utf-8" }).trim();
      if (!out) return [];
      return out.split("\n").map((s) => parseInt(s, 10)).filter((n) => !isNaN(n));
    } catch {
      return [];
    }
  }

  /** Check if loop is managed by launchctl (KeepAlive). */
  function isLaunchctlManaged(): boolean {
    try {
      // If the service is loaded, launchctl list will show it.
      const out = execFileSync("launchctl", ["list", "com.loop.agent"], { encoding: "utf-8" });
      return out.includes("com.loop.agent");
    } catch {
      return false;
    }
  }

  if (daemonProcess) {
    // App-managed daemon: just kill it.
    daemonProcess.kill("SIGTERM");
    daemonProcess = null;
    managedDaemon = false;
    // Wait for port to be free.
    for (let i = 0; i < 10; i++) {
      await new Promise((r) => setTimeout(r, 500));
      if (findListeningPids().length === 0) break;
    }
  } else if (isLaunchctlManaged()) {
    // System-installed daemon via launchctl: use kickstart to restart.
    // This tells launchd to stop and immediately re-launch the service,
    // avoiding the race where KeepAlive respawns before we can start our own.
    console.log("Restarting daemon via launchctl kickstart");
    try {
      const uid = process.getuid?.();
      execFileSync("launchctl", ["kickstart", "-k", `gui/${uid}/com.loop.agent`], { encoding: "utf-8" });
    } catch (err) {
      console.warn("launchctl kickstart failed:", err);
    }
  } else {
    // System daemon not managed by launchctl: kill and restart manually.
    const pids = findListeningPids();
    for (const pid of pids) {
      try { process.kill(pid, "SIGTERM"); } catch { /* already dead */ }
    }
    for (let i = 0; i < 10; i++) {
      await new Promise((r) => setTimeout(r, 500));
      if (findListeningPids().length === 0) break;
    }
  }

  // Wait for daemon to become healthy (launchctl restart or ensureDaemon).
  if (!isLaunchctlManaged()) {
    await ensureDaemon();
  } else {
    // Wait for launchctl-managed daemon to come back up.
    for (let i = 0; i < 30; i++) {
      await new Promise((r) => setTimeout(r, 500));
      if (await isDaemonRunning()) break;
    }
  }

  return {
    running: await isDaemonRunning(),
    managed: managedDaemon,
    binaryPath: findLoopBinary(),
  };
});
