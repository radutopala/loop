import path from "node:path";
import { pathToFileURL } from "node:url";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type MainProcessDeps, startMainProcess } from "./mainProcess";

type Fn = (...args: any[]) => any;

const DIR = "/app/dist-electron";
const ENTRY = path.join(DIR, "../dist/index.html");
const ENTRY_URL = pathToFileURL(ENTRY).href;
const DEV = "http://localhost:5173";
const CONFIG = "/home/u/.loop/config.json";
const TOKEN = "/data/loop/api-token";
const BIN = "/res/bin/loop";
const PLUGIN_FRAME = "http://127.0.0.1:8222/c/eyJrIjoicGx1Z2luIn0.c2ln/ui/index.html";

/** Records handlers by event name and calls them on emit. */
class Emitter {
  handlers = new Map<string, Fn[]>();
  on = vi.fn((name: string, fn: Fn) => {
    this.handlers.set(name, [...(this.handlers.get(name) ?? []), fn]);
    return this;
  });
  emit(name: string, ...args: any[]): any[] {
    return (this.handlers.get(name) ?? []).map((fn) => fn(...args));
  }
}

class FakeWebContents extends Emitter {
  loading = false;
  openHandler: Fn = () => undefined;
  send = vi.fn();
  executeJavaScript = vi.fn(async (_code: string): Promise<unknown> => "");
  isLoading = vi.fn(() => this.loading);
  setWindowOpenHandler = vi.fn((h: Fn) => {
    this.openHandler = h;
  });
}

interface Opts {
  platform?: NodeJS.Platform;
  env?: NodeJS.ProcessEnv;
  defaultApp?: boolean;
  argv?: string[];
  files?: Record<string, string>;
  lock?: boolean;
  dock?: boolean;
}

/** Starts the main process against fake Electron, fs and network. */
function setup(opts: Opts = {}) {
  const platform = opts.platform ?? "darwin";
  const env = opts.env ?? {};
  const files: Record<string, string> = { ...opts.files };
  const windows: FakeWindow[] = [];

  class FakeWindow extends Emitter {
    static getAllWindows = () => [...windows];
    static getFocusedWindow = () => windows.find((w) => w.focused) ?? null;
    static fromWebContents = (wc: FakeWebContents) => windows.find((w) => w.webContents === wc) ?? null;
    webContents = new FakeWebContents();
    focused = false;
    visible: boolean;
    minimized = false;
    maximize = vi.fn();
    loadURL = vi.fn();
    loadFile = vi.fn();
    isMinimized = vi.fn(() => this.minimized);
    restore = vi.fn(() => {
      this.minimized = false;
    });
    show = vi.fn(() => {
      this.visible = true;
      this.emit("show");
    });
    hide = vi.fn(() => {
      this.visible = false;
      this.emit("hide");
    });
    focus = vi.fn();
    isFocused = vi.fn(() => this.focused);
    isVisible = vi.fn(() => this.visible);
    flashFrame = vi.fn();
    setVisibleOnAllWorkspaces = vi.fn();
    setPosition = vi.fn();
    setAlwaysOnTop = vi.fn();
    setBounds = vi.fn();
    constructor(public opts: Record<string, any>) {
      super();
      this.visible = opts.show !== false;
      windows.push(this);
    }
  }

  const trays: FakeTray[] = [];
  class FakeTray extends Emitter {
    setToolTip = vi.fn();
    setTitle = vi.fn();
    setImage = vi.fn();
    getBounds = vi.fn(() => ({ x: 1000, y: 0, width: 24, height: 24 }));
    constructor(public image: unknown) {
      super();
      trays.push(this);
    }
  }

  const app = Object.assign(new Emitter(), {
    name: "Loop",
    setName: vi.fn(),
    setAsDefaultProtocolClient: vi.fn(),
    requestSingleInstanceLock: vi.fn(() => opts.lock ?? true),
    quit: vi.fn(),
    dock: opts.dock === false ? undefined : { setIcon: vi.fn(), bounce: vi.fn((_t: string) => 7), cancelBounce: vi.fn() },
    commandLine: { appendSwitch: vi.fn() },
    getPath: vi.fn((_name: string) => "/data"),
    getVersion: vi.fn(() => "1.0.0"),
  });
  const listeners = new Map<string, Fn>();
  const handlers = new Map<string, Fn>();
  const ipcMain = {
    on: vi.fn((name: string, fn: Fn) => listeners.set(name, fn)),
    handle: vi.fn((name: string, fn: Fn) => handlers.set(name, fn)),
  };
  const dialog = {
    showMessageBox: vi.fn(async (..._args: unknown[]) => ({ response: 0 })),
    showOpenDialog: vi.fn(async (_opts: unknown) => ({ canceled: false, filePaths: ["/proj"] })),
  };
  const Menu = { setApplicationMenu: vi.fn(), buildFromTemplate: vi.fn((t: unknown) => t) };
  const nativeImage = { createFromPath: vi.fn((p: string) => ({ path: p, setTemplateImage: vi.fn() })) };
  const workArea = { x: 0, y: 25, width: 1440, height: 875 };
  const screen = { getDisplayNearestPoint: vi.fn((_p: unknown) => ({ workArea })) };
  const session = { defaultSession: { setPermissionRequestHandler: vi.fn(), setPermissionCheckHandler: vi.fn() } };
  const shell = { openExternal: vi.fn(async (_url: string) => {}) };
  const systemPreferences = { getMediaAccessStatus: vi.fn((_t: string) => "granted"), askForMediaAccess: vi.fn(async (_t: string) => true) };
  const autoUpdater = Object.assign(new Emitter(), {
    autoDownload: true,
    autoInstallOnAppQuit: true,
    checkForUpdates: vi.fn(async (): Promise<unknown> => null),
    downloadUpdate: vi.fn(async () => []),
    quitAndInstall: vi.fn(),
  });
  const fs = {
    existsSync: vi.fn((p: string) => p in files),
    readFileSync: vi.fn((p: string) => {
      if (!(p in files)) throw new Error("ENOENT");
      return files[p]!;
    }),
    readlinkSync: vi.fn(() => {
      throw new Error("ENOENT");
    }),
    unlinkSync: vi.fn(),
    symlinkSync: vi.fn(),
    mkdirSync: vi.fn(),
    writeFileSync: vi.fn(),
  };
  // Nothing on PATH unless a test says otherwise.
  const execFileSync = vi.fn<MainProcessDeps["execFileSync"]>(() => {
    throw new Error("not found");
  });
  const fetch = vi.fn<typeof globalThis.fetch>(async () => new Response(null, { status: 200 }));
  const sleep = vi.fn(async (_ms: number) => {});

  startMainProcess({
    electron: { app, BrowserWindow: FakeWindow, dialog, ipcMain, Menu, nativeImage, screen, session, shell, systemPreferences, Tray: FakeTray } as unknown as MainProcessDeps["electron"],
    autoUpdater: autoUpdater as unknown as MainProcessDeps["autoUpdater"],
    fs: fs as unknown as MainProcessDeps["fs"],
    execFileSync,
    fetch,
    homedir: () => "/home/u",
    sleep,
    dirname: DIR,
    process: { platform, env, defaultApp: opts.defaultApp, argv: opts.argv ?? ["electron"], execPath: "/bin/electron", resourcesPath: "/res" },
  });

  const send = (name: string, ...args: unknown[]) => listeners.get(name)!(...args);
  const invoke = (name: string, ...args: unknown[]) => handlers.get(name)!({}, ...args);
  const ready = async () => {
    await Promise.all(app.emit("ready"));
  };
  const mainWins = () => windows.filter((w) => w.opts.type !== "panel");
  const inbox = () => windows.find((w) => w.opts.type === "panel")!;
  const menuItem = (menu: string, label: string) => {
    const template = Menu.setApplicationMenu.mock.calls[0]![0] as { label?: string; role?: string; submenu?: { label?: string; click?: Fn }[] }[];
    return template.find((m) => (m.label ?? m.role) === menu)!.submenu!.find((i) => i.label === label)!;
  };
  return {
    app,
    windows,
    trays,
    FakeWindow,
    ipcMain,
    dialog,
    Menu,
    nativeImage,
    screen,
    session,
    shell,
    systemPreferences,
    autoUpdater,
    fs,
    files,
    execFileSync,
    fetch,
    sleep,
    send,
    invoke,
    ready,
    mainWins,
    inbox,
    menuItem,
  };
}

type Env = ReturnType<typeof setup>;

let log: ReturnType<typeof vi.spyOn>;
let warn: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
  log = vi.spyOn(console, "log").mockImplementation(() => {});
  warn = vi.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** A started, ready app with its first window. */
async function started(opts: Opts = {}) {
  const env = setup(opts);
  await env.ready();
  return { ...env, win: env.mainWins()[0]! };
}

const ev = () => ({ preventDefault: vi.fn() });

describe("startup", () => {
  it("names the app and registers the protocol", () => {
    const { app } = setup();
    expect(app.setName).toHaveBeenCalledWith("Loop");
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("loop");
  });

  it("registers the protocol with the script path in dev", () => {
    const { app } = setup({ defaultApp: true, argv: ["electron", "main.js"] });
    expect(app.setAsDefaultProtocolClient).toHaveBeenCalledWith("loop", "/bin/electron", [path.resolve("main.js")]);
  });

  it("registers nothing in dev without a script", () => {
    const { app } = setup({ defaultApp: true, argv: ["electron"] });
    expect(app.setAsDefaultProtocolClient).not.toHaveBeenCalled();
  });

  it.each([
    [true, 0],
    [false, 1],
  ])("quits when another instance holds the lock (lock=%s)", (lock, quits) => {
    const { app } = setup({ lock });
    expect(app.quit).toHaveBeenCalledTimes(quits);
  });

  it("sets the dock icon on macOS", () => {
    expect(setup().app.dock!.setIcon).toHaveBeenCalledWith("/app/dist/loop-macos.png");
    expect(setup({ env: { VITE_DEV_SERVER_URL: DEV } }).app.dock!.setIcon).toHaveBeenCalledWith("/app/public/loop-macos.png");
    expect(setup({ platform: "linux" }).app.dock!.setIcon).not.toHaveBeenCalled();
    expect(() => setup({ dock: false })).not.toThrow();
  });

  it.each([
    [{ VITE_DEV_SERVER_URL: DEV }, "9223"],
    [{ VITE_DEV_SERVER_URL: DEV, LOOP_DEBUG_PORT: "9999" }, "9999"],
  ])("opens the debugging port in dev", (env, port) => {
    const { app } = setup({ env });
    expect(app.commandLine.appendSwitch.mock.calls).toEqual([
      ["remote-debugging-address", "127.0.0.1"],
      ["remote-debugging-port", port],
    ]);
  });

  it("keeps the debugging port closed when packaged", () => {
    expect(setup().app.commandLine.appendSwitch).not.toHaveBeenCalled();
  });
});

describe("ready", () => {
  it("in a packaged build starts the daemon, opens the page file, the inbox and the updater", async () => {
    const { win, execFileSync, inbox, autoUpdater, session, Menu } = await started();
    expect(execFileSync).toHaveBeenCalledWith("which", ["loop"], { encoding: "utf-8" });
    expect(win.loadFile).toHaveBeenCalledWith(ENTRY, { hash: undefined });
    expect(win.maximize).toHaveBeenCalled();
    expect(win.opts).toMatchObject({
      title: "Loop",
      icon: "/app/dist/loop-macos.png",
      width: 1200,
      height: 800,
      minWidth: 900,
      minHeight: 400,
      backgroundColor: "#212121",
      titleBarStyle: "hiddenInset",
      webPreferences: { preload: path.join(DIR, "preload.cjs"), contextIsolation: true, nodeIntegration: false, sandbox: false },
    });
    expect(inbox()).toBeDefined();
    expect(autoUpdater.autoDownload).toBe(false);
    expect(autoUpdater.autoInstallOnAppQuit).toBe(false);
    expect(autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
    expect(session.defaultSession.setPermissionRequestHandler).toHaveBeenCalled();
    expect(Menu.setApplicationMenu).toHaveBeenCalled();
  });

  it("in dev loads the dev server and skips the daemon and the updater", async () => {
    const { win, execFileSync, autoUpdater, inbox } = await started({ env: { VITE_DEV_SERVER_URL: DEV } });
    expect(win.loadURL).toHaveBeenCalledWith(DEV);
    expect(execFileSync).not.toHaveBeenCalled();
    expect(autoUpdater.checkForUpdates).not.toHaveBeenCalled();
    expect(inbox().loadURL).toHaveBeenCalledWith(`${DEV}?window=inbox`);
  });

  it("has no inbox off macOS, and a hidden title bar", async () => {
    const { windows, win, trays } = await started({ platform: "linux" });
    expect(windows).toHaveLength(1);
    expect(trays).toHaveLength(0);
    expect(win.opts.titleBarStyle).toBe("hidden");
  });

  it.each([
    ['{"desktop": {"theme": "claude"}}', "#FAF6F1"],
    ['{"desktop": {"theme": "light"}}', "#ffffff"],
    ['{"desktop": {"theme": "dark"}}', "#212121"],
    ['{"desktop": {}}', "#212121"],
  ])("takes the background from the theme in %s", async (config, color) => {
    const { win, inbox } = await started({ files: { [CONFIG]: config } });
    expect(win.opts.backgroundColor).toBe(color);
    expect(inbox().opts.backgroundColor).toBe(color);
  });

  it("opens a channel that arrived before ready in the first window", async () => {
    const env = setup();
    env.app.emit("open-url", ev(), "loop://channel/ch-1");
    await env.ready();
    expect(env.mainWins()[0]!.loadFile).toHaveBeenCalledWith(ENTRY, { hash: "ch-1" });
  });

  it("opens a channel that arrived before ready in dev", async () => {
    const env = setup({ env: { VITE_DEV_SERVER_URL: DEV } });
    env.app.emit("open-url", ev(), "loop://channel/ch-1");
    await env.ready();
    expect(env.mainWins()[0]!.loadURL).toHaveBeenCalledWith(`${DEV}#ch-1`);
  });

  it("grants media by the app-page rule", async () => {
    const { session } = await started();
    const request = session.defaultSession.setPermissionRequestHandler.mock.calls[0]![0] as Fn;
    const callback = vi.fn();
    request({}, "media", callback, { isMainFrame: true, requestingUrl: ENTRY_URL, mediaTypes: ["audio"] });
    request({}, "media", callback, { isMainFrame: true, requestingUrl: ENTRY_URL, mediaTypes: ["video"] });
    expect(callback.mock.calls).toEqual([[true], [false]]);
    const check = session.defaultSession.setPermissionCheckHandler.mock.calls[0]![0] as Fn;
    expect(check({}, "media", "", { isMainFrame: true, requestingUrl: ENTRY_URL, mediaType: "audio" })).toBe(true);
    expect(check({}, "media", "", { isMainFrame: false, requestingUrl: ENTRY_URL, mediaType: "audio" })).toBe(false);
  });
});

describe("main window", () => {
  it("sends external popups to the OS and denies all of them", async () => {
    const { win, shell } = await started();
    expect(win.webContents.openHandler({ url: "https://example.com/" })).toEqual({ action: "deny" });
    expect(win.webContents.openHandler({ url: "about:blank" })).toEqual({ action: "deny" });
    expect(win.webContents.openHandler({ url: "not a url" })).toEqual({ action: "deny" });
    expect(shell.openExternal.mock.calls).toEqual([["https://example.com/"]]);
  });

  it.each([
    ["the app page", `${ENTRY_URL}#ch`, false, false],
    ["an external page", "mailto:a@b.c", true, true],
    ["another file", "file:///etc/passwd", true, false],
  ])("guards navigation to %s", async (_name, url, prevented, opened) => {
    const { win, shell } = await started();
    const e = ev();
    win.webContents.emit("will-navigate", e, url);
    expect(e.preventDefault).toHaveBeenCalledTimes(prevented ? 1 : 0);
    expect(shell.openExternal).toHaveBeenCalledTimes(opened ? 1 : 0);
  });

  it.each([
    ["the main frame", { isMainFrame: true, frame: { url: PLUGIN_FRAME }, url: "https://x/" }, false],
    ["a gone frame", { isMainFrame: false, frame: null, url: "https://x/" }, false],
    ["the app pointing a frame elsewhere", { isMainFrame: false, frame: { url: PLUGIN_FRAME }, initiator: { parent: null }, url: "https://x/" }, false],
    ["a plugin frame leaving its bundle", { isMainFrame: false, frame: { url: PLUGIN_FRAME }, url: "https://x/" }, true],
    ["a plugin frame leaving from a subframe", { isMainFrame: false, frame: { url: PLUGIN_FRAME }, initiator: { parent: {} }, url: "https://x/" }, true],
    ["a plugin frame inside its bundle", { isMainFrame: false, frame: { url: PLUGIN_FRAME }, url: PLUGIN_FRAME.replace("index.html", "b.html") }, false],
  ])("frame navigation by %s", async (_name, details, prevented) => {
    const { win } = await started();
    const preventDefault = vi.fn();
    win.webContents.emit("will-frame-navigate", { ...details, preventDefault });
    expect(preventDefault).toHaveBeenCalledTimes(prevented ? 1 : 0);
  });

  it("does nothing on load without a pending channel", async () => {
    const { win } = await started();
    win.webContents.emit("did-finish-load");
    expect(win.webContents.executeJavaScript).not.toHaveBeenCalled();
  });
});

/** Lets pending promise chains settle. */
async function flush() {
  for (let i = 0; i < 10; i++) await new Promise((r) => setImmediate(r));
}

const hashJS = (id: string) => `window.location.hash = ${JSON.stringify(id)}`;

describe("opening a channel", () => {
  it("navigates the window that sent open-channel", async () => {
    const { send, win } = await started();
    win.minimized = true;
    send("open-channel", { sender: win.webContents }, "ch-2");
    expect(win.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("ch-2"));
    expect(win.restore).toHaveBeenCalled();
    expect(win.show).toHaveBeenCalled();
    expect(win.focus).toHaveBeenCalled();
  });

  it("ignores an empty channel or an unknown sender", async () => {
    const { send, win } = await started();
    send("open-channel", { sender: win.webContents }, "");
    send("open-channel", { sender: new FakeWebContents() }, "ch-2");
    expect(win.webContents.executeJavaScript).not.toHaveBeenCalled();
  });

  it("opens a loop:// link in the ready window", async () => {
    const { app, win } = await started();
    const e = ev();
    app.emit("open-url", e, "loop://channel/ch-3");
    expect(e.preventDefault).toHaveBeenCalled();
    expect(win.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("ch-3"));
    expect(win.restore).not.toHaveBeenCalled();
  });

  it("ignores a link without a channel", async () => {
    const { app, win } = await started();
    app.emit("open-url", ev(), "loop://");
    app.emit("open-url", ev(), "not a url");
    expect(win.webContents.executeJavaScript).not.toHaveBeenCalled();
  });

  it("holds a link until a loading window finishes", async () => {
    const { app, win } = await started();
    win.webContents.loading = true;
    app.emit("open-url", ev(), "loop://channel/ch-4");
    expect(win.webContents.executeJavaScript).not.toHaveBeenCalled();
    win.webContents.loading = false;
    win.webContents.emit("did-finish-load");
    win.webContents.emit("did-finish-load");
    expect(win.webContents.executeJavaScript.mock.calls).toEqual([[hashJS("ch-4")]]);
  });

  it("picks the focused window, but not the inbox", async () => {
    const { app, menuItem, mainWins, inbox } = await started();
    await menuItem("File", "New Window").click!();
    const [first, second] = mainWins();
    second!.focused = true;
    app.emit("open-url", ev(), "loop://channel/a");
    expect(second!.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("a"));
    second!.focused = false;
    inbox().focused = true;
    app.emit("open-url", ev(), "loop://channel/b");
    expect(first!.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("b"));
  });

  it("from a second instance navigates and brings the window forward", async () => {
    const { app, win } = await started();
    win.minimized = true;
    app.emit("second-instance", {}, ["loop", "--flag", "loop://channel/ch-5"]);
    expect(win.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("ch-5"));
    expect(win.focus).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["no link", ["loop"]],
    ["a link without a channel", ["loop", "loop://"]],
  ])("from a second instance with %s only brings the window forward", async (_name, argv) => {
    const { app, win } = await started();
    win.minimized = true;
    app.emit("second-instance", {}, argv);
    expect(win.webContents.executeJavaScript).not.toHaveBeenCalled();
    expect(win.restore).toHaveBeenCalled();
    expect(win.focus).toHaveBeenCalledTimes(1);
  });

  it("from a second instance does nothing without a window", () => {
    const { app } = setup();
    expect(() => app.emit("second-instance", {}, ["loop", "loop://channel/x"])).not.toThrow();
  });
});

describe("menu", () => {
  it("has the app menu and Close on macOS", async () => {
    const { Menu } = await started();
    const template = Menu.setApplicationMenu.mock.calls[0]![0] as { label?: string; role?: string; submenu?: { role?: string }[] }[];
    expect(template.map((m) => m.label ?? m.role)).toEqual(["Loop", "File", "Edit", "View", "Developer", "windowMenu"]);
    expect(template[1]!.submenu!.at(-1)).toEqual({ role: "close" });
  });

  it("has no app menu and Quit elsewhere", async () => {
    const { Menu } = await started({ platform: "linux" });
    const template = Menu.setApplicationMenu.mock.calls[0]![0] as { label?: string; role?: string; submenu?: { role?: string }[] }[];
    expect(template.map((m) => m.label ?? m.role)).toEqual(["File", "Edit", "View", "Developer", "windowMenu"]);
    expect(template[0]!.submenu!.at(-1)).toEqual({ role: "quit" });
  });

  describe("Check for Updates", () => {
    it("is unavailable in dev", async () => {
      const { menuItem, dialog, autoUpdater } = await started({ env: { VITE_DEV_SERVER_URL: DEV } });
      await menuItem("Loop", "Check for Updates…").click!();
      expect(autoUpdater.checkForUpdates).not.toHaveBeenCalled();
      expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "Updates are not available in dev mode.", buttons: ["OK"] });
    });

    it.each([
      ["no result", null],
      ["no update info", {}],
      ["the same version", { updateInfo: { version: "1.0.0" } }],
    ])("says up to date for %s", async (_name, result) => {
      const { menuItem, dialog, autoUpdater } = await started();
      autoUpdater.checkForUpdates.mockResolvedValue(result);
      await menuItem("Loop", "Check for Updates…").click!();
      expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "You're up to date!", detail: "Loop 1.0.0 is the latest version.", buttons: ["OK"] });
    });

    it("says nothing for a newer version (the update event reports it)", async () => {
      const { menuItem, dialog, autoUpdater } = await started();
      autoUpdater.checkForUpdates.mockResolvedValue({ updateInfo: { version: "2.0.0" } });
      await menuItem("Loop", "Check for Updates…").click!();
      expect(dialog.showMessageBox).not.toHaveBeenCalled();
    });

    it("reports a failed check", async () => {
      const { menuItem, dialog, autoUpdater } = await started();
      autoUpdater.checkForUpdates.mockRejectedValue(new Error("offline"));
      await menuItem("Loop", "Check for Updates…").click!();
      expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "Unable to check for updates.", detail: "Please try again later.", buttons: ["OK"] });
    });
  });

  it("Install CLI reports a missing binary", async () => {
    const { menuItem, dialog } = await started();
    menuItem("Loop", "Install CLI").click!();
    expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "CLI binary not found.", detail: "Not available in dev mode.", buttons: ["OK"] });
  });

  it("Settings opens the settings in the window", async () => {
    const { menuItem, win, windows } = await started();
    menuItem("Loop", "Settings…").click!();
    expect(win.webContents.send).toHaveBeenCalledWith("open-settings");
    windows.splice(0);
    expect(() => menuItem("Loop", "Settings…").click!()).not.toThrow();
  });

  it.each([
    ["the focused window's channel", async () => "ch-9", "ch-9"],
    ["no channel", async () => "", undefined],
    ["a failed read", async () => Promise.reject(new Error("gone")), undefined],
  ])("New Window opens on %s", async (_name, read, hash) => {
    const { menuItem, win, mainWins } = await started();
    win.focused = true;
    win.webContents.executeJavaScript.mockImplementation(read);
    await menuItem("File", "New Window").click!();
    expect(win.webContents.executeJavaScript).toHaveBeenCalledWith("window.location.hash.slice(1)");
    expect(mainWins()[1]!.loadFile).toHaveBeenCalledWith(ENTRY, { hash });
  });

  it("New Window without a focused window opens the start page", async () => {
    const { menuItem, win, mainWins } = await started();
    await menuItem("File", "New Window").click!();
    expect(win.webContents.executeJavaScript).not.toHaveBeenCalled();
    expect(mainWins()[1]!.loadFile).toHaveBeenCalledWith(ENTRY, { hash: undefined });
  });

  describe("Rotate API Token", () => {
    const rotate = async (env: Env) => {
      env.menuItem("Developer", "Rotate API Token…").click!();
      await flush();
    };

    it("does nothing when cancelled", async () => {
      const env = await started();
      env.dialog.showMessageBox.mockResolvedValueOnce({ response: 1 });
      await rotate(env);
      expect(env.dialog.showMessageBox).toHaveBeenCalledWith(env.win, expect.objectContaining({ message: "Rotate the API token?", cancelId: 1, defaultId: 1 }));
      expect(env.fetch).not.toHaveBeenCalled();
    });

    it("posts the rotation with the token and confirms", async () => {
      const env = await started({ files: { [TOKEN]: "tok" } });
      env.fetch.mockResolvedValue(new Response(null, { status: 204 }));
      await rotate(env);
      expect(env.fetch).toHaveBeenCalledWith("http://localhost:8222/api/auth/rotate", { method: "POST", headers: { Authorization: "Bearer tok" }, signal: expect.any(AbortSignal) });
      expect(env.dialog.showMessageBox).toHaveBeenLastCalledWith({ message: "API token rotated", buttons: ["OK"] });
    });

    it("asks without a parent when there's no window", async () => {
      const env = await started();
      env.windows.splice(0);
      env.fetch.mockResolvedValue(new Response(null, { status: 204 }));
      await rotate(env);
      expect(env.dialog.showMessageBox.mock.calls[0]).toEqual([expect.objectContaining({ message: "Rotate the API token?" })]);
    });

    it("shows the daemon's error when it's running", async () => {
      const env = await started();
      env.fetch.mockResolvedValueOnce(new Response("bad \n", { status: 500, statusText: "Internal Server Error" }));
      await rotate(env);
      expect(env.dialog.showMessageBox).toHaveBeenLastCalledWith({
        type: "error",
        message: "Couldn't rotate the API token",
        detail: "Error: 500 Internal Server Error: bad",
        buttons: ["OK"],
      });
    });

    it("says the daemon isn't running", async () => {
      const env = await started();
      env.fetch.mockRejectedValue(new Error("refused"));
      await rotate(env);
      expect(env.dialog.showMessageBox).toHaveBeenLastCalledWith(expect.objectContaining({ detail: "The daemon isn't running. Start it, or run `loop api:rotate-token`." }));
    });
  });
});

describe("auto-updater", () => {
  it("tells every window how the update goes", async () => {
    const { autoUpdater, win, inbox, invoke } = await started();
    autoUpdater.emit("update-available", { version: "2.0.0" });
    const available = { available: true, version: "2.0.0", downloading: false, downloaded: false };
    expect(win.webContents.send).toHaveBeenLastCalledWith("update-status", available);
    expect(inbox().webContents.send).toHaveBeenLastCalledWith("update-status", available);
    autoUpdater.emit("download-progress");
    expect(win.webContents.send).toHaveBeenLastCalledWith("update-status", { ...available, downloading: true });
    autoUpdater.emit("update-downloaded");
    expect(win.webContents.send).toHaveBeenLastCalledWith("update-status", { ...available, downloaded: true });
    autoUpdater.emit("error", new Error("disk"));
    expect(win.webContents.send).toHaveBeenLastCalledWith("update-status", { ...available, downloaded: true, error: "Error: disk" });
    expect(warn).toHaveBeenCalledWith("Auto-updater error:", expect.any(Error));
    expect(await invoke("get-update-status")).toEqual({ ...available, downloaded: true, error: "Error: disk" });
    autoUpdater.emit("update-not-available");
    expect(await invoke("get-update-status")).toEqual({ available: false, downloading: false, downloaded: false });
  });

  it("checks again every 30 minutes and swallows failures", async () => {
    const env = setup();
    env.autoUpdater.checkForUpdates.mockRejectedValue(new Error("offline"));
    await env.ready();
    expect(env.autoUpdater.checkForUpdates).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(30 * 60 * 1000);
    expect(env.autoUpdater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it("downloads on request", async () => {
    const { invoke, autoUpdater } = await started();
    await invoke("download-update");
    expect(autoUpdater.downloadUpdate).toHaveBeenCalled();
  });
});

describe("app lifecycle", () => {
  it.each([
    ["darwin", 0],
    ["linux", 1],
  ] as const)("on %s quits when the last window closes %i times", (platform, quits) => {
    const { app } = setup({ platform });
    app.emit("window-all-closed");
    expect(app.quit).toHaveBeenCalledTimes(quits);
  });

  it("before quitting leaves the daemon running by default", () => {
    const { app, execFileSync } = setup({ files: { [BIN]: "" } });
    app.emit("before-quit");
    expect(execFileSync).not.toHaveBeenCalled();
  });

  it("before quitting stops the daemon when asked to", () => {
    const { app, execFileSync } = setup({ files: { [BIN]: "", [CONFIG]: '{"desktop": {"stop_daemon_on_quit": true}}' } });
    execFileSync.mockReturnValue("");
    app.emit("before-quit");
    expect(execFileSync).toHaveBeenCalledWith(BIN, ["daemon:stop"], { encoding: "utf-8", timeout: 15_000 });
    expect(log).toHaveBeenCalledWith(`Stopping loop daemon via: ${BIN} daemon:stop`);
  });

  it("before quitting warns when stopping fails", () => {
    const { app } = setup({ files: { [BIN]: "", [CONFIG]: '{"desktop": {"stop_daemon_on_quit": true}}' } });
    app.emit("before-quit");
    expect(warn).toHaveBeenCalledWith("daemon:stop failed:", expect.any(Error));
  });

  it("before quitting skips the stop without a binary", () => {
    const { app, execFileSync } = setup({ files: { [CONFIG]: '{"desktop": {"stop_daemon_on_quit": true}}' } });
    app.emit("before-quit");
    expect(execFileSync.mock.calls.map((c) => c[0])).toEqual(["which"]);
  });

  it("reopens a window on activate only when none is open", async () => {
    const { app, mainWins } = await started();
    app.emit("activate");
    expect(mainWins()).toHaveLength(1);
    mainWins()[0]!.webContents = new FakeWebContents();
    const env = setup({ platform: "linux" });
    env.app.emit("activate");
    expect(env.mainWins()).toHaveLength(1);
  });

  it("counts the inbox out on activate", async () => {
    const env = await started();
    env.windows.splice(env.windows.indexOf(env.win), 1);
    env.app.emit("activate");
    expect(env.mainWins()).toHaveLength(1);
    expect(env.mainWins()[0]).not.toBe(env.win);
  });
});

describe("dock bouncing", () => {
  it("bounces once per unfocused stretch for a finished turn", async () => {
    const { send, app } = await started();
    send("turn-ended");
    send("turn-ended");
    expect(app.dock!.bounce.mock.calls).toEqual([["informational"]]);
    app.emit("browser-window-focus");
    send("turn-ended");
    expect(app.dock!.bounce).toHaveBeenCalledTimes(2);
  });

  it("doesn't bounce for a turn while focused", async () => {
    const { send, app, win } = await started();
    win.focused = true;
    send("turn-ended");
    expect(app.dock!.bounce).not.toHaveBeenCalled();
  });

  it("flashes the frame for a turn off macOS", async () => {
    const { send, win } = await started({ platform: "linux" });
    send("turn-ended");
    expect(win.flashFrame).toHaveBeenCalledWith(true);
  });

  it("copes with no dock on a turn", async () => {
    const { send } = await started({ dock: false });
    expect(() => send("turn-ended")).not.toThrow();
  });

  it("keeps bouncing while an approval waits, until it's resolved", async () => {
    const { send, app } = await started();
    send("approval-needed", {}, "r1");
    expect(app.dock!.bounce).toHaveBeenLastCalledWith("critical");
    send("approval-needed", {}, "r1");
    send("approval-needed", {}, "r2");
    expect(app.dock!.bounce).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(2000 * 9);
    expect(app.dock!.bounce).toHaveBeenCalledTimes(10);
    expect(log).toHaveBeenCalledWith("[bounce] tick=10 size=2 pending=[r1,r2]");
    expect(log).toHaveBeenCalledWith("[bounce] approval-needed reqId=r1 duplicate=true size=1 pending=[r1]");
    send("approval-resolved", {}, "r1");
    expect(app.dock!.cancelBounce).not.toHaveBeenCalled();
    send("approval-resolved", {}, "nope");
    expect(log).toHaveBeenCalledWith("[bounce] approval-resolved reqId=nope known=false size=1 pending=[r2]");
    send("approval-resolved", {}, "r2");
    expect(app.dock!.cancelBounce).toHaveBeenCalledWith(7);
    await vi.advanceTimersByTimeAsync(2000);
    expect(app.dock!.bounce).toHaveBeenCalledTimes(10);
    send("approval-resolved");
    expect(log).toHaveBeenCalledWith("[bounce] approval-resolved reqId=<none> known=false size=0 pending=[]");
  });

  it("skips ticks while a window is focused", async () => {
    const { send, app, win } = await started();
    send("approval-needed", {}, "r1");
    win.focused = true;
    await vi.advanceTimersByTimeAsync(2000);
    expect(app.dock!.bounce).toHaveBeenCalledTimes(1);
  });

  it("doesn't start without a request id or while focused", async () => {
    const { send, app, win } = await started();
    send("approval-needed");
    expect(log).toHaveBeenCalledWith("[bounce] approval-needed reqId=<none> duplicate=false size=0 pending=[]");
    win.focused = true;
    send("approval-needed", {}, "r1");
    expect(app.dock!.bounce).not.toHaveBeenCalled();
  });

  it("flashes frames off macOS and stops them when resolved", async () => {
    const { send, win } = await started({ platform: "linux" });
    send("approval-needed", {}, "r1");
    expect(win.flashFrame).toHaveBeenCalledWith(true);
    send("approval-resolved", {}, "r1");
    expect(win.flashFrame).toHaveBeenLastCalledWith(false);
  });

  it("copes with no dock for approvals", async () => {
    const { send } = await started({ dock: false });
    send("approval-needed", {}, "r1");
    expect(() => send("approval-resolved", {}, "r1")).not.toThrow();
  });

  it("reconciles the pending set with the renderer's list", async () => {
    const { send, app } = await started();
    send("approval-needed", {}, "r1");
    send("approval-needed", {}, "r4");
    send("reconcile-approvals", {}, ["r2", "r4", "r3"]);
    expect(log).toHaveBeenCalledWith("[bounce] reconcile-approvals incoming=3 dropped=1 size=3 pending=[r4,r2,r3]");
    expect(app.dock!.cancelBounce).not.toHaveBeenCalled();
    send("reconcile-approvals", {}, "junk");
    expect(log).toHaveBeenCalledWith("[bounce] reconcile-approvals incoming=0 dropped=3 size=0 pending=[]");
    expect(app.dock!.cancelBounce).toHaveBeenCalledWith(7);
  });

  it("stops on focus and starts again on blur", async () => {
    const { send, app, win } = await started();
    send("approval-needed", {}, "r1");
    app.emit("browser-window-focus");
    await vi.advanceTimersByTimeAsync(2000);
    expect(app.dock!.bounce).toHaveBeenCalledTimes(1);
    // A focus with no loop running clears nothing.
    app.emit("browser-window-focus");
    send("approval-resolved", {}, "r1");
    expect(app.dock!.cancelBounce).not.toHaveBeenCalled();

    send("approval-needed", {}, "r2");
    app.emit("browser-window-focus");
    win.focused = true;
    app.emit("browser-window-blur");
    await vi.advanceTimersByTimeAsync(50);
    expect(app.dock!.bounce).toHaveBeenCalledTimes(2);
    win.focused = false;
    app.emit("browser-window-blur");
    await vi.advanceTimersByTimeAsync(50);
    expect(app.dock!.bounce).toHaveBeenCalledTimes(3);
    expect(log).toHaveBeenCalledWith("[bounce] window blurred, pending size=1 pending=[r2]");
  });

  it("does nothing on blur with nothing pending", async () => {
    const { app } = await started();
    app.emit("browser-window-blur");
    await vi.advanceTimersByTimeAsync(50);
    expect(app.dock!.bounce).not.toHaveBeenCalled();
  });
});

describe("renderer requests", () => {
  it("broadcasts a theme change", async () => {
    const { send, win, inbox } = await started();
    send("set-theme", {}, "light");
    expect(win.webContents.send).toHaveBeenCalledWith("theme-changed", "light");
    expect(inbox().webContents.send).toHaveBeenCalledWith("theme-changed", "light");
  });

  it.each([
    [42, false],
    ["file:///etc", false],
    ["http://a/", true],
    ["https://a/", true],
  ])("opens %s externally: %s", async (url, opened) => {
    const { invoke, shell } = await started();
    await invoke("open-external", url);
    expect(shell.openExternal).toHaveBeenCalledTimes(opened ? 1 : 0);
  });

  describe("install-update", () => {
    it("removes the image, restarts the daemon and installs", async () => {
      const { invoke, fetch, execFileSync, autoUpdater, inbox } = await started({ files: { [BIN]: "", [TOKEN]: "tok" } });
      execFileSync.mockReturnValue("");
      await invoke("install-update");
      expect(fetch).toHaveBeenCalledWith("http://localhost:8222/api/image", { method: "DELETE", headers: { Authorization: "Bearer tok" }, signal: expect.any(AbortSignal) });
      expect(execFileSync).toHaveBeenCalledWith(BIN, ["daemon:restart"], { encoding: "utf-8", timeout: 30_000 });
      expect(autoUpdater.quitAndInstall).toHaveBeenCalledWith(false, true);
      // Quitting now: closing the inbox no longer hides it instead.
      const e = ev();
      inbox().emit("close", e);
      expect(e.preventDefault).not.toHaveBeenCalled();
    });

    it("aborts a slow image removal and goes on when the restart fails", async () => {
      const { invoke, fetch, autoUpdater } = await started({ files: { [BIN]: "" } });
      fetch.mockImplementation(
        (_url, init) =>
          new Promise((_resolve, reject) => {
            init!.signal!.addEventListener("abort", () => reject(new Error("aborted")));
          }),
      );
      const done = invoke("install-update");
      await vi.advanceTimersByTimeAsync(30_000);
      await done;
      expect(warn).toHaveBeenCalledWith("daemon:restart before update failed:", expect.any(Error));
      expect(autoUpdater.quitAndInstall).toHaveBeenCalled();
    });

    it("installs without a binary", async () => {
      const { invoke, execFileSync, autoUpdater } = await started();
      await invoke("install-update");
      expect(execFileSync.mock.calls.every((c) => c[0] === "which")).toBe(true);
      expect(autoUpdater.quitAndInstall).toHaveBeenCalled();
    });
  });

  it.each([
    [{ canceled: true, filePaths: ["/x"] }, null],
    [{ canceled: false, filePaths: [] }, null],
    [{ canceled: false, filePaths: ["/proj", "/other"] }, "/proj"],
  ])("picks a directory from %o", async (result, want) => {
    const { invoke, dialog } = await started();
    dialog.showOpenDialog.mockResolvedValue(result);
    expect(await invoke("show-open-directory-dialog")).toBe(want);
    expect(dialog.showOpenDialog).toHaveBeenCalledWith({ properties: ["openDirectory"], title: "Select project directory" });
  });

  describe("onboard-local", () => {
    it("needs the binary", async () => {
      const { invoke } = await started();
      expect(await invoke("onboard-local", "/proj")).toEqual({ ok: false, error: "loop binary not found" });
    });

    it("runs onboarding in the directory", async () => {
      const { invoke, execFileSync } = await started({ files: { [BIN]: "" } });
      execFileSync.mockReturnValue("done");
      expect(await invoke("onboard-local", "/proj")).toEqual({ ok: true, output: "done" });
      expect(execFileSync).toHaveBeenCalledWith(BIN, ["onboard:local", "--api-url", "http://localhost:8222", "--platform", "local"], {
        cwd: "/proj",
        encoding: "utf-8",
        timeout: 15_000,
      });
    });

    it("reports a failure", async () => {
      const { invoke } = await started({ files: { [BIN]: "" } });
      expect(await invoke("onboard-local", "/proj")).toEqual({ ok: false, error: "Error: not found" });
    });
  });

  describe("microphone-access", () => {
    it("is granted off macOS", async () => {
      const { invoke, systemPreferences } = await started({ platform: "linux" });
      expect(await invoke("microphone-access", true)).toBe("granted");
      expect(systemPreferences.getMediaAccessStatus).not.toHaveBeenCalled();
    });

    it.each([
      ["denied", true, true, "denied"],
      ["not-determined", false, true, "not-determined"],
      ["not-determined", true, true, "granted"],
      ["not-determined", true, false, "denied"],
    ])("from %s with ask=%s (answer %s) gives %s", async (status, ask, answer, want) => {
      const { invoke, systemPreferences } = await started();
      systemPreferences.getMediaAccessStatus.mockReturnValue(status);
      systemPreferences.askForMediaAccess.mockResolvedValue(answer);
      expect(await invoke("microphone-access", ask)).toBe(want);
    });
  });

  it("gives the API URL and token", async () => {
    const { invoke } = await started({ files: { [TOKEN]: "tok\n" }, env: { LOOP_API_URL: "http://env:1" } });
    expect(await invoke("get-api-url")).toBe("http://env:1");
    expect(await invoke("get-api-token")).toBe("tok");
  });

  it("gives the daemon info", async () => {
    const { invoke, fetch } = await started({ files: { [BIN]: "" } });
    expect(await invoke("get-daemon-info")).toEqual({ running: true, binaryPath: BIN });
    expect(fetch).toHaveBeenCalledWith("http://localhost:8222/api/health", expect.anything());
  });

  describe("restart-daemon", () => {
    it("restarts and waits until healthy", async () => {
      const { invoke, execFileSync, fetch, sleep } = await started({ files: { [BIN]: "" } });
      execFileSync.mockReturnValue("");
      sleep.mockClear();
      fetch.mockResolvedValueOnce(new Response(null, { status: 503 }));
      expect(await invoke("restart-daemon")).toEqual({ running: true, binaryPath: BIN });
      expect(execFileSync).toHaveBeenCalledWith(BIN, ["daemon:restart"], { encoding: "utf-8", timeout: 30_000 });
      expect(sleep.mock.calls).toEqual([[500], [500]]);
    });

    it("gives up after 30 tries and warns when the restart fails", async () => {
      const { invoke, fetch, sleep } = await started({ files: { [BIN]: "" } });
      sleep.mockClear();
      fetch.mockRejectedValue(new Error("refused"));
      expect(await invoke("restart-daemon")).toEqual({ running: false, binaryPath: BIN });
      expect(warn).toHaveBeenCalledWith("daemon:restart failed:", expect.any(Error));
      expect(sleep).toHaveBeenCalledTimes(30);
    });

    it("only waits without a binary", async () => {
      const { invoke } = await started();
      expect(await invoke("restart-daemon")).toEqual({ running: true, binaryPath: null });
    });
  });
});

describe("menu bar inbox", () => {
  const sender = (env: Env) => ({ sender: env.inbox().webContents });
  const stranger = { sender: new FakeWebContents() };

  it("sets up the tray icon and the hidden panel", async () => {
    const env = await started();
    const tray = env.trays[0]!;
    const [plain, alert] = env.nativeImage.createFromPath.mock.results.map((r) => r.value);
    expect(plain.path).toBe("/app/dist/tray/trayTemplate.png");
    expect(plain.setTemplateImage).toHaveBeenCalledWith(true);
    expect(alert.path).toBe("/app/dist/tray/trayAlert.png");
    expect(tray.image).toBe(plain);
    expect(tray.setToolTip).toHaveBeenCalledWith("Loop: nothing needs you");
    const inbox = env.inbox();
    expect(inbox.opts).toMatchObject({ width: 380, height: 520, show: false, frame: false, alwaysOnTop: true, webPreferences: { backgroundThrottling: false } });
    expect(inbox.setVisibleOnAllWorkspaces).toHaveBeenCalledWith(true, { visibleOnFullScreen: true, skipTransformProcessType: true });
    expect(inbox.loadFile).toHaveBeenCalledWith(ENTRY, { query: { window: "inbox" } });
  });

  it("uses the dev tray images in dev", async () => {
    const env = await started({ env: { VITE_DEV_SERVER_URL: DEV } });
    expect(env.nativeImage.createFromPath).toHaveBeenCalledWith("/app/public/tray/trayTemplate.png");
  });

  it("sends its links to the OS and never navigates", async () => {
    const env = await started();
    const wc = env.inbox().webContents;
    expect(wc.openHandler({ url: "https://a/" })).toEqual({ action: "deny" });
    expect(wc.openHandler({ url: "about:blank" })).toEqual({ action: "deny" });
    const e1 = ev();
    wc.emit("will-navigate", e1, "https://b/");
    const e2 = ev();
    wc.emit("will-navigate", e2, ENTRY_URL);
    expect(e1.preventDefault).toHaveBeenCalled();
    expect(e2.preventDefault).toHaveBeenCalled();
    expect(env.shell.openExternal.mock.calls).toEqual([["https://a/"], ["https://b/"]]);
  });

  it("tells its page when it shows and hides", async () => {
    const env = await started();
    const inbox = env.inbox();
    inbox.show();
    inbox.hide();
    expect(inbox.webContents.send.mock.calls).toEqual([
      ["inbox-shown", true],
      ["inbox-shown", false],
    ]);
  });

  it("hides when closed, until quitting", async () => {
    const env = await started();
    const inbox = env.inbox();
    inbox.visible = true;
    const e = ev();
    inbox.emit("close", e);
    expect(e.preventDefault).toHaveBeenCalled();
    expect(inbox.visible).toBe(false);
    env.app.emit("before-quit");
    const e2 = ev();
    inbox.emit("close", e2);
    expect(e2.preventDefault).not.toHaveBeenCalled();
  });

  it("shows the count and flashes from the page's report", async () => {
    const env = await started();
    const tray = env.trays[0]!;
    const [plain, alert] = env.nativeImage.createFromPath.mock.results.map((r) => r.value);
    env.send("inbox-report", stranger, { count: 3, flash: true });
    env.send("inbox-report", sender(env), "junk");
    expect(tray.setTitle).not.toHaveBeenCalled();
    env.send("inbox-report", sender(env), { count: 3, flash: true });
    expect(tray.setTitle).toHaveBeenCalledWith("3", { fontType: "monospacedDigit" });
    expect(tray.setToolTip).toHaveBeenLastCalledWith("Loop: 3 waiting on you");
    expect(tray.setImage).toHaveBeenLastCalledWith(alert);
    await vi.advanceTimersByTimeAsync(600);
    expect(tray.setImage).toHaveBeenLastCalledWith(plain);
    env.send("inbox-report", sender(env), { count: 0, flash: false });
    expect(tray.setTitle).toHaveBeenLastCalledWith("", { fontType: "monospacedDigit" });
    expect(tray.setToolTip).toHaveBeenLastCalledWith("Loop: nothing needs you");
    const calls = tray.setImage.mock.calls.length;
    await vi.advanceTimersByTimeAsync(1200);
    expect(tray.setImage).toHaveBeenCalledTimes(calls);
  });

  it("moves where the page drags it", async () => {
    const env = await started();
    env.send("inbox-move", stranger, { x: 1, y: 2 });
    env.send("inbox-move", sender(env), "junk");
    env.send("inbox-move", sender(env), { x: 10.4, y: 20.6 });
    expect(env.inbox().setPosition.mock.calls).toEqual([[10, 21]]);
  });

  it("floats when pinned", async () => {
    const env = await started();
    env.send("inbox-set-pinned", stranger, true);
    env.send("inbox-set-pinned", sender(env), true);
    env.send("inbox-set-pinned", sender(env), "yes");
    expect(env.inbox().setAlwaysOnTop.mock.calls).toEqual([
      [true, "floating"],
      [true, "pop-up-menu"],
    ]);
  });

  it("closes on request from its own page", async () => {
    const env = await started();
    const inbox = env.inbox();
    inbox.visible = true;
    env.send("inbox-close", stranger);
    expect(inbox.visible).toBe(true);
    env.send("inbox-close", sender(env));
    expect(inbox.visible).toBe(false);
  });

  describe("opening a channel from it", () => {
    it("ignores other senders and bad channels", async () => {
      const env = await started();
      env.send("inbox-open-channel", stranger, "c");
      env.send("inbox-open-channel", sender(env), 5);
      env.send("inbox-open-channel", sender(env), "");
      expect(env.win.webContents.executeJavaScript).not.toHaveBeenCalled();
      expect(env.inbox().hide).not.toHaveBeenCalled();
    });

    it("hides the unpinned inbox and navigates the window", async () => {
      const env = await started();
      env.send("inbox-open-channel", sender(env), "c1");
      expect(env.inbox().hide).toHaveBeenCalled();
      expect(env.win.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("c1"));
    });

    it("keeps a pinned inbox up and holds the channel for a loading window", async () => {
      const env = await started();
      env.send("inbox-set-pinned", sender(env), true);
      env.win.webContents.loading = true;
      env.send("inbox-open-channel", sender(env), "c2");
      expect(env.inbox().hide).not.toHaveBeenCalled();
      expect(env.mainWins()).toHaveLength(1);
      env.win.webContents.emit("did-finish-load");
      expect(env.win.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("c2"));
    });

    it("opens a window when there is none", async () => {
      const env = await started();
      env.windows.splice(env.windows.indexOf(env.win), 1);
      env.send("inbox-open-channel", sender(env), "c3");
      const win = env.mainWins()[0]!;
      expect(win).not.toBe(env.win);
      win.webContents.emit("did-finish-load");
      expect(win.webContents.executeJavaScript).toHaveBeenCalledWith(hashJS("c3"));
    });
  });

  describe("tray clicks", () => {
    it("open the inbox under the clicked icon", async () => {
      const env = await started();
      const tray = env.trays[0]!;
      const inbox = env.inbox();
      tray.emit("click", {}, { x: 1000, y: 0, width: 24, height: 24 });
      expect(env.screen.getDisplayNearestPoint).toHaveBeenCalledWith({ x: 1000, y: 0 });
      expect(inbox.setBounds).toHaveBeenCalledWith({ x: 822, y: 29, width: 380, height: 520 });
      expect(inbox.show).toHaveBeenCalled();
      expect(inbox.focus).toHaveBeenCalled();
      expect(tray.getBounds).not.toHaveBeenCalled();
      tray.emit("right-click", {}, { x: 0, y: 0, width: 0, height: 0 });
      expect(inbox.visible).toBe(false);
    });

    it.each([
      ["empty bounds", { x: 0, y: 0, width: 0, height: 0 }],
      ["no bounds", undefined],
    ])("fall back to the tray's bounds for %s", async (_name, bounds) => {
      const env = await started();
      env.trays[0]!.emit("right-click", {}, bounds);
      expect(env.trays[0]!.getBounds).toHaveBeenCalled();
      expect(env.inbox().setBounds).toHaveBeenCalledWith({ x: 822, y: 29, width: 380, height: 520 });
    });

    it("bring a pinned inbox forward instead of hiding it, and keep its place", async () => {
      const env = await started();
      const inbox = env.inbox();
      env.send("inbox-set-pinned", sender(env), true);
      env.trays[0]!.emit("click", {}, undefined);
      expect(inbox.setBounds).not.toHaveBeenCalled();
      expect(inbox.visible).toBe(true);
      env.trays[0]!.emit("click", {}, undefined);
      expect(inbox.visible).toBe(true);
      expect(inbox.focus).toHaveBeenCalledTimes(2);
      // Pinned, losing focus keeps it up.
      inbox.emit("blur");
      expect(inbox.visible).toBe(true);
    });

    it("don't reopen right after a blur hid it", async () => {
      const env = await started();
      const inbox = env.inbox();
      // Hidden: a blur does nothing.
      inbox.emit("blur");
      env.trays[0]!.emit("click", {}, undefined);
      expect(inbox.visible).toBe(true);
      inbox.emit("blur");
      expect(inbox.visible).toBe(false);
      env.trays[0]!.emit("click", {}, undefined);
      expect(inbox.visible).toBe(false);
      vi.advanceTimersByTime(300);
      env.trays[0]!.emit("click", {}, undefined);
      expect(inbox.visible).toBe(true);
    });
  });
});
