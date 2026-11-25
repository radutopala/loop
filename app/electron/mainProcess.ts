import type fsType from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import type { BrowserWindow, Tray } from "electron";
import type { AppUpdater } from "electron-updater";
import { type ExecFileSync, loopDaemon } from "./daemon";
import { Flasher, INBOX_QUERY, INBOX_SIZE, inboxBounds, parseInboxMove, parseInboxReport, trayTitle } from "./inboxTray";
import { loopConfig } from "./loopConfig";
import { allowPermissionCheck, allowPermissionRequest, isAppEntryURL } from "./mediaPermission";
import { allowFrameNavigation } from "./pluginFrameGuard";

type ElectronMain = typeof import("electron");

export interface MainProcessDeps {
  electron: Pick<ElectronMain, "app" | "BrowserWindow" | "dialog" | "ipcMain" | "Menu" | "nativeImage" | "screen" | "session" | "shell" | "systemPreferences" | "Tray">;
  autoUpdater: AppUpdater;
  fs: Pick<typeof fsType, "existsSync" | "readFileSync" | "readlinkSync" | "unlinkSync" | "symlinkSync" | "mkdirSync" | "writeFileSync">;
  execFileSync: ExecFileSync;
  fetch: typeof fetch;
  homedir: () => string;
  sleep: (ms: number) => Promise<void>;
  /** The directory of the built main script, where preload.cjs sits. */
  dirname: string;
  /** The parts of Electron's process the main process reads. */
  process: {
    platform: NodeJS.Platform;
    env: NodeJS.ProcessEnv;
    defaultApp?: boolean;
    argv: string[];
    execPath: string;
    resourcesPath: string;
  };
}

/** Sets up the Electron main process: protocol, windows, menu, IPC, the
 *  auto-updater, dock bouncing and the menu bar inbox. */
export function startMainProcess(deps: MainProcessDeps): void {
  const { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, screen, session, shell, systemPreferences, Tray } = deps.electron;
  const { autoUpdater, execFileSync, dirname: __dirname, sleep } = deps;
  const { platform, env } = deps.process;

  app.setName("Loop");

  const config = loopConfig({
    fs: deps.fs,
    homedir: deps.homedir,
    appData: () => app.getPath("appData"),
    env,
  });
  const { readApiToken, authHeaders, resolveApiUrl, readDesktopConfig } = config;
  const { findLoopBinary, isDaemonRunning, ensureDaemon, installCLI } = loopDaemon({
    fs: deps.fs,
    execFileSync,
    fetch: deps.fetch,
    dialog,
    platform,
    env,
    resourcesPath: deps.process.resourcesPath,
    config,
    sleep,
  });

  const PROTOCOL = "loop";

  if (deps.process.defaultApp) {
    // In dev mode, register with the path to the electron binary + script.
    if (deps.process.argv.length >= 2) {
      app.setAsDefaultProtocolClient(PROTOCOL, deps.process.execPath, [path.resolve(deps.process.argv[1]!)]);
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
  const iconMacos = env.VITE_DEV_SERVER_URL ? path.join(__dirname, "../public/loop-macos.png") : path.join(__dirname, "../dist/loop-macos.png");
  if (platform === "darwin") {
    app.dock?.setIcon(iconMacos);
  }

  const VITE_DEV_SERVER_URL = env.VITE_DEV_SERVER_URL;

  // Dev builds accept Chrome DevTools Protocol clients on localhost, to measure
  // or drive the app from outside. Packaged builds never open this port.
  if (VITE_DEV_SERVER_URL) {
    app.commandLine.appendSwitch("remote-debugging-address", "127.0.0.1");
    app.commandLine.appendSwitch("remote-debugging-port", env.LOOP_DEBUG_PORT || "9223");
  }

  // The packaged app's page, and the URL the window may show: the dev server
  // in dev, the file URL of that page otherwise.
  const appEntryFile = path.join(__dirname, "../dist/index.html");
  const appEntryURL = VITE_DEV_SERVER_URL || pathToFileURL(appEntryFile).href;

  /** Whether url is an http(s) or mailto URL, the only kinds handed to the OS. */
  function isExternalURL(url: string): boolean {
    try {
      const { protocol } = new URL(url);
      return protocol === "http:" || protocol === "https:" || protocol === "mailto:";
    } catch {
      return false;
    }
  }

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
      titleBarStyle: platform === "darwin" ? "hiddenInset" : "hidden",
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
      win.loadFile(appEntryFile, {
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

    // No in-app popups: external links (http/https/mailto) go to the OS,
    // and everything else (about:blank, file:, custom schemes, …) is denied.
    win.webContents.setWindowOpenHandler(({ url }) => {
      if (isExternalURL(url)) shell.openExternal(url);
      return { action: "deny" };
    });

    // In-window navigation guard: the window only ever shows the app itself.
    // Plain `<a href="https://…">` clicks (e.g. from the README rendered via
    // dangerouslySetInnerHTML) would otherwise replace the React app with the
    // external page, with no way back, so they go to the OS browser. Any other
    // navigation away from the app entry (file:, custom schemes, other dev
    // server paths, …) is blocked. Hash changes are in-page and never get here.
    win.webContents.on("will-navigate", (event, url) => {
      if (isAppEntryURL(url, appEntryURL)) return;
      event.preventDefault();
      if (isExternalURL(url)) shell.openExternal(url);
    });

    // A plugin's panel or card frame may move around its own bundle but not
    // leave it: CSP can't stop a frame navigating itself, and a page left
    // that way could carry what the bridge gave it out in the URL. The app
    // itself (the top frame) still points frames at fresh links.
    win.webContents.on("will-frame-navigate", (details) => {
      if (details.isMainFrame || !details.frame) return;
      if (details.initiator && details.initiator.parent === null) return;
      if (!allowFrameNavigation(details.frame.url, details.url)) details.preventDefault();
    });

    return win;
  }

  /** The app's windows, without the menu bar inbox. */
  function mainWindows(): BrowserWindow[] {
    return BrowserWindow.getAllWindows().filter((w) => w !== inboxWin);
  }

  /** Returns the most recently focused window, or the first available one. */
  function getFocusedOrLastWindow(): BrowserWindow | null {
    const focused = BrowserWindow.getFocusedWindow();
    if (focused && focused !== inboxWin) return focused;
    return mainWindows()[0] ?? null;
  }

  function navigateToChannel(channelId: string, win = getFocusedOrLastWindow()) {
    if (!channelId || !win) return;
    // Set hash directly on the page — triggers the renderer's hashchange listener.
    // This avoids IPC timing issues where the listener isn't mounted yet.
    win.webContents.executeJavaScript(`window.location.hash = ${JSON.stringify(channelId)}`);
    if (win.isMinimized()) win.restore();
    win.show();
    win.focus();
  }

  // A clicked notification opens its channel in the window that showed it.
  ipcMain.on("open-channel", (event, channelId: string) => {
    navigateToChannel(channelId, BrowserWindow.fromWebContents(event.sender));
  });

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

  /** What `loop api:rotate-token` does, from the menu: the daemon writes a new
   *  owner token and switches to it. The app and the CLI read it again on their
   *  next 401; browser tabs opened with a `#loop_token=` link need a new link. */
  async function rotateApiToken() {
    const win = getFocusedOrLastWindow();
    const opts: Electron.MessageBoxOptions = {
      type: "warning",
      message: "Rotate the API token?",
      detail: "The daemon switches to a new token at once. The desktop app and the CLI pick it up on their own; browser tabs opened with a loop_token link need a new one from `loop app:url`.",
      buttons: ["Rotate", "Cancel"],
      defaultId: 1,
      cancelId: 1,
    };
    const { response } = await (win ? dialog.showMessageBox(win, opts) : dialog.showMessageBox(opts));
    if (response !== 0) return;
    try {
      const res = await deps.fetch(`${resolveApiUrl()}/api/auth/rotate`, { method: "POST", headers: authHeaders(), signal: AbortSignal.timeout(10_000) });
      if (res.status !== 204) throw new Error(`${res.status} ${res.statusText}: ${(await res.text()).trim()}`);
    } catch (err) {
      const running = await isDaemonRunning();
      dialog.showMessageBox({
        type: "error",
        message: "Couldn't rotate the API token",
        detail: running ? String(err) : "The daemon isn't running. Start it, or run `loop api:rotate-token`.",
        buttons: ["OK"],
      });
      return;
    }
    dialog.showMessageBox({ message: "API token rotated", buttons: ["OK"] });
  }

  function buildMenu() {
    const isMac = platform === "darwin";
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
                } catch {
                  /* ignore */
                }
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
        submenu: [{ role: "undo" }, { role: "redo" }, { type: "separator" }, { role: "cut" }, { role: "copy" }, { role: "paste" }, { role: "selectAll" }],
      },
      {
        label: "View",
        submenu: [
          { role: "reload" },
          { role: "forceReload" },
          { type: "separator" },
          { role: "resetZoom" },
          { role: "zoomIn" },
          { role: "zoomOut" },
          { type: "separator" },
          { role: "togglefullscreen" },
        ],
      },
      {
        label: "Developer",
        submenu: [{ role: "toggleDevTools" }, { type: "separator" }, { label: "Rotate API Token…", click: () => void rotateApiToken() }],
      },
      {
        role: "windowMenu",
      },
    ];

    Menu.setApplicationMenu(Menu.buildFromTemplate(template));
  }

  // --- Auto-updater ---

  let updateStatus: { available: boolean; version?: string; downloading: boolean; downloaded: boolean; error?: string } = {
    available: false,
    downloading: false,
    downloaded: false,
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
    setInterval(
      () => {
        autoUpdater.checkForUpdates().catch(() => {});
      },
      30 * 60 * 1000,
    );
  }

  /** Narrows media permissions to the microphone for the app's own page
   *  (dictation); everything else stays granted as by Electron's default. */
  function setupPermissionHandlers() {
    session.defaultSession.setPermissionRequestHandler((_wc, permission, callback, details) => {
      callback(allowPermissionRequest(permission, details, appEntryURL));
    });
    session.defaultSession.setPermissionCheckHandler((_wc, permission, _origin, details) => allowPermissionCheck(permission, details, appEntryURL));
  }

  app.on("ready", async () => {
    setupPermissionHandlers();
    buildMenu();
    if (!VITE_DEV_SERVER_URL) {
      await ensureDaemon();
    }
    createWindow(pendingChannelId || undefined);
    pendingChannelId = null;
    if (platform === "darwin") setupInbox();

    if (!VITE_DEV_SERVER_URL) {
      setupAutoUpdater();
    }
  });

  app.on("window-all-closed", () => {
    if (platform !== "darwin") {
      app.quit();
    }
  });

  app.on("before-quit", () => {
    quitting = true;
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
    if (mainWindows().length === 0) {
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
    if (platform === "darwin") {
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

    if (platform !== "darwin") {
      for (const w of wins) w.flashFrame(true);
      return;
    }

    if (approvalBounceInterval) return;
    console.log(`[bounce] starting approval bounce loop size=${pendingApprovals.size} pending=${pendingSnapshot()}`);
    let tickCount = 0;
    // The interval stops whenever the set empties, so a tick always has
    // something pending.
    const tick = () => {
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
    if (platform === "darwin") {
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
      await deps.fetch(`${url}/api/image`, { method: "DELETE", headers: authHeaders(), signal: controller.signal });
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
    // On macOS the install closes every window before before-quit runs, so
    // the flag goes up here: the inbox popup would otherwise take its close
    // for a cmd+W, hide instead and so cancel the quit.
    quitting = true;
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

  // Microphone access for dictation. macOS asks the user once per app (with
  // ask, when still undecided); elsewhere the OS doesn't gate it here.
  ipcMain.handle("microphone-access", async (_event, ask?: boolean) => {
    if (platform !== "darwin") return "granted";
    const status = systemPreferences.getMediaAccessStatus("microphone");
    if (status !== "not-determined" || !ask) return status;
    return (await systemPreferences.askForMediaAccess("microphone")) ? "granted" : "denied";
  });

  ipcMain.handle("get-api-url", () => {
    return resolveApiUrl();
  });

  ipcMain.handle("get-api-token", () => readApiToken());

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
      await sleep(500);
      if (await isDaemonRunning()) break;
    }

    return {
      running: await isDaemonRunning(),
      binaryPath: findLoopBinary(),
    };
  });

  // --- Menu bar inbox ---
  //
  // A tray icon in the menu bar counts what waits on the owner across every
  // project and flashes while something holding an agent hasn't been seen.
  // Clicking it opens the inbox: the app's page in its inbox mode, in a
  // small window under the icon. Unpinned, it hides when it loses focus, like
  // a menu; pinned, it stays up on every Space until unpinned.

  let inboxWin: BrowserWindow | null = null;
  let inboxPinned = false;
  let quitting = false;
  // When the inbox last hid on blur: a click on the icon blurs the inbox
  // before the click arrives, and shouldn't then open it again.
  let inboxBlurHiddenAt = 0;

  const trayDir = VITE_DEV_SERVER_URL ? path.join(__dirname, "../public/tray") : path.join(__dirname, "../dist/tray");

  function setupInbox() {
    const plain = nativeImage.createFromPath(path.join(trayDir, "trayTemplate.png"));
    plain.setTemplateImage(true);
    const alert = nativeImage.createFromPath(path.join(trayDir, "trayAlert.png"));
    const tray = new Tray(plain);
    tray.setToolTip("Loop: nothing needs you");
    const flasher = new Flasher((on) => tray.setImage(on ? alert : plain), { setInterval, clearInterval: (id) => clearInterval(id as NodeJS.Timeout) });
    // The bounds are of the icon clicked: with a menu bar on each display,
    // tray.getBounds() may give another display's.
    tray.on("click", (_e, bounds) => toggleInbox(inbox, tray, bounds));
    tray.on("right-click", (_e, bounds) => toggleInbox(inbox, tray, bounds));

    const inbox = createInboxWindow();
    inboxWin = inbox;

    ipcMain.on("inbox-report", (event, msg: unknown) => {
      if (event.sender !== inbox.webContents) return;
      const report = parseInboxReport(msg);
      if (!report) return;
      tray.setTitle(trayTitle(report.count), { fontType: "monospacedDigit" });
      tray.setToolTip(report.count > 0 ? `Loop: ${report.count} waiting on you` : "Loop: nothing needs you");
      flasher.set(report.flash);
    });

    // The inbox drags itself: a drag area doesn't move this window.
    ipcMain.on("inbox-move", (event, msg: unknown) => {
      if (event.sender !== inbox.webContents) return;
      const pos = parseInboxMove(msg);
      if (pos) inbox.setPosition(pos.x, pos.y);
    });

    ipcMain.on("inbox-set-pinned", (event, pinned: unknown) => {
      if (event.sender !== inbox.webContents) return;
      inboxPinned = pinned === true;
      inbox.setAlwaysOnTop(true, inboxPinned ? "floating" : "pop-up-menu");
    });

    ipcMain.on("inbox-open-channel", (event, channelId: unknown) => {
      if (event.sender !== inbox.webContents || typeof channelId !== "string" || !channelId) return;
      if (!inboxPinned) inbox.hide();
      const win = getFocusedOrLastWindow();
      if (win && !win.webContents.isLoading()) {
        navigateToChannel(channelId, win);
        return;
      }
      pendingChannelId = channelId;
      if (!win) createWindow();
    });

    ipcMain.on("inbox-close", (event) => {
      if (event.sender === inbox.webContents) inbox.hide();
    });
  }

  function createInboxWindow(): BrowserWindow {
    const win = new BrowserWindow({
      width: INBOX_SIZE.width,
      height: INBOX_SIZE.height,
      minWidth: 300,
      minHeight: 200,
      show: false,
      frame: false,
      // A panel takes focus without making Loop the active app, which would
      // bring the main window forward, on another display too.
      type: "panel",
      resizable: true,
      movable: true,
      minimizable: false,
      maximizable: false,
      fullscreenable: false,
      skipTaskbar: true,
      alwaysOnTop: true,
      backgroundColor: initialBackgroundColor(),
      webPreferences: {
        preload: path.join(__dirname, "preload.cjs"),
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: false,
        // It keeps the count and the flashing current while hidden.
        backgroundThrottling: false,
      },
    });
    // Pinned or not, it shows on the Space of the display it's on, a
    // full-screen app's too. Without skipTransformProcessType, Electron briefly
    // switches the whole app to an accessory process, which hides the other
    // Loop windows and the Dock icon.
    win.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true, skipTransformProcessType: true });
    if (VITE_DEV_SERVER_URL) {
      win.loadURL(`${VITE_DEV_SERVER_URL}?${INBOX_QUERY}`);
    } else {
      const [key, value] = INBOX_QUERY.split("=");
      win.loadFile(appEntryFile, { query: { [key!]: value! } });
    }
    win.webContents.setWindowOpenHandler(({ url }) => {
      if (isExternalURL(url)) shell.openExternal(url);
      return { action: "deny" };
    });
    win.webContents.on("will-navigate", (event, url) => {
      event.preventDefault();
      if (isExternalURL(url)) shell.openExternal(url);
    });
    // The page can't tell itself: without background throttling it always
    // reads as visible.
    win.on("show", () => win.webContents.send("inbox-shown", true));
    win.on("hide", () => win.webContents.send("inbox-shown", false));
    win.on("blur", () => {
      if (inboxPinned || !win.isVisible()) return;
      inboxBlurHiddenAt = Date.now();
      win.hide();
    });
    // Closing it (cmd+W) hides it; it lives as long as the app.
    win.on("close", (event) => {
      if (quitting) return;
      event.preventDefault();
      win.hide();
    });
    return win;
  }

  function toggleInbox(win: BrowserWindow, tray: Tray, clicked?: Electron.Rectangle) {
    // A click on the icon doesn't take focus from the inbox, so an open one
    // may still be focused here, or not. A pinned one stays: the click brings
    // it forward, and its × closes it.
    if (win.isVisible()) {
      if (inboxPinned) win.focus();
      else win.hide();
      return;
    }
    if (!win.isVisible() && Date.now() - inboxBlurHiddenAt < 300) return;
    if (!inboxPinned) {
      const trayBounds = clicked?.width ? clicked : tray.getBounds();
      const display = screen.getDisplayNearestPoint({ x: trayBounds.x, y: trayBounds.y });
      win.setBounds(inboxBounds(trayBounds, INBOX_SIZE, display.workArea));
    }
    win.show();
    win.focus();
  }
}
