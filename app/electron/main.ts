import { app, BrowserWindow, ipcMain, Menu } from "electron";
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
  const win = new BrowserWindow({
    title: "Loop",
    icon: iconMacos, // undefined in production — uses .icns from electron-builder
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

app.on("ready", () => {
  buildMenu();
  createWindow(pendingChannelId || undefined);
  pendingChannelId = null;
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("activate", () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
});

ipcMain.handle("get-api-url", () => {
  return process.env.LOOP_API_URL ?? "http://localhost:8222";
});
