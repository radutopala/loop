import { app, BrowserWindow, ipcMain } from "electron";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

app.setName("Loop");

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

// In dev mode, set the dock icon explicitly since we're running the
// generic Electron binary which shows the Electron icon by default.
// In production, the .icns from electron-builder handles the app icon.
const iconPng = process.env.VITE_DEV_SERVER_URL
  ? path.join(__dirname, "../public/loop.png")
  : undefined;
if (iconPng && process.platform === "darwin") {
  app.dock?.setIcon(iconPng);
}

let mainWindow: BrowserWindow | null = null;

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

function createWindow(hash?: string) {
  mainWindow = new BrowserWindow({
    title: "Loop",
    icon: iconPng, // undefined in production — uses .icns from electron-builder
    width: 1200,
    height: 800,
    minWidth: 900,
    minHeight: 400,
    titleBarStyle: "hiddenInset",
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  const fragment = hash ? `#${hash}` : "";

  if (VITE_DEV_SERVER_URL) {
    mainWindow.loadURL(`${VITE_DEV_SERVER_URL}${fragment}`);
  } else {
    mainWindow.loadFile(path.join(__dirname, "../dist/index.html"), {
      hash: hash || undefined,
    });
  }

  // Handle protocol URLs that arrived while the page was loading.
  mainWindow.webContents.on("did-finish-load", () => {
    if (pendingChannelId) {
      navigateToChannel(pendingChannelId);
      pendingChannelId = null;
    }
  });

  mainWindow.on("closed", () => {
    mainWindow = null;
  });
}

function navigateToChannel(channelId: string) {
  if (!channelId || !mainWindow) return;
  // Set hash directly on the page — triggers the renderer's hashchange listener.
  // This avoids IPC timing issues where the listener isn't mounted yet.
  mainWindow.webContents.executeJavaScript(
    `window.location.hash = ${JSON.stringify(channelId)}`,
  );
  if (mainWindow.isMinimized()) mainWindow.restore();
  mainWindow.focus();
}

let pendingChannelId: string | null = null;

// macOS: open-url fires when app is already running or being launched.
// It can fire before OR after the ready event.
app.on("open-url", (event, url) => {
  event.preventDefault();
  const channelId = parseChannelId(url);
  if (!channelId) return;
  if (mainWindow && !mainWindow.webContents.isLoading()) {
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
  if (mainWindow) {
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  }
});

app.on("ready", () => {
  createWindow(pendingChannelId || undefined);
  pendingChannelId = null;
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("activate", () => {
  if (mainWindow === null) {
    createWindow();
  }
});

ipcMain.handle("get-api-url", () => {
  return process.env.LOOP_API_URL ?? "http://localhost:8222";
});
