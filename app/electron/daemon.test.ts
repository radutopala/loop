import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type DaemonDeps, loopDaemon } from "./daemon";

const RES = "/res";
const BIN = "/res/bin/loop";

interface Opts {
  platform?: NodeJS.Platform;
  files?: Record<string, string>;
  links?: Record<string, string>;
  env?: NodeJS.ProcessEnv;
  hasConfig?: boolean;
}

/** A daemon over in-memory files, a scripted execFileSync and fetch. */
function setup(opts: Opts = {}) {
  const files: Record<string, string> = { ...opts.files };
  const links: Record<string, string> = { ...opts.links };
  const fs = {
    existsSync: vi.fn((p: string) => p in files),
    readFileSync: vi.fn((p: string) => {
      if (!(p in files)) throw new Error("ENOENT");
      return files[p]!;
    }),
    readlinkSync: vi.fn((p: string) => {
      if (!(p in links)) throw new Error("ENOENT");
      return links[p]!;
    }),
    unlinkSync: vi.fn((p: string) => {
      if (!(p in links)) throw new Error("ENOENT");
      delete links[p];
    }),
    symlinkSync: vi.fn((target: string, p: string) => {
      links[p] = target;
    }),
    mkdirSync: vi.fn(),
    writeFileSync: vi.fn((p: string, data: string) => {
      files[p] = data;
    }),
  };
  const execFileSync = vi.fn<DaemonDeps["execFileSync"]>(() => "");
  const fetch = vi.fn<typeof globalThis.fetch>(async () => new Response(null, { status: 200 }));
  const dialog = { showMessageBox: vi.fn(async () => ({ response: 0, checkboxChecked: false })) };
  const sleep = vi.fn(async () => {});
  const d = loopDaemon({
    fs: fs as never,
    execFileSync,
    fetch,
    dialog: dialog as never,
    platform: opts.platform ?? "darwin",
    env: opts.env ?? {},
    resourcesPath: RES,
    config: { hasLoopConfig: () => opts.hasConfig ?? true, resolveApiUrl: () => "http://api" },
    sleep,
  });
  return { d, fs, files, links, execFileSync, fetch, dialog, sleep };
}

let log: ReturnType<typeof vi.spyOn>;
let warn: ReturnType<typeof vi.spyOn>;
beforeEach(() => {
  log = vi.spyOn(console, "log").mockImplementation(() => {});
  warn = vi.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("bundledBinaryPath", () => {
  it.each([
    ["the bin layout", "darwin", { [BIN]: "" }, BIN],
    ["the old flat layout", "darwin", { "/res/loop": "" }, "/res/loop"],
    ["the Windows name", "win32", { "/res/bin/loop.exe": "" }, "/res/bin/loop.exe"],
    ["nothing bundled", "linux", {}, null],
  ] as const)("finds %s", (_name, platform, files, want) => {
    expect(setup({ platform, files }).d.bundledBinaryPath()).toBe(want);
  });
});

describe("findLoopBinary", () => {
  it("prefers the bundled binary", () => {
    const { d, execFileSync } = setup({ files: { [BIN]: "" } });
    expect(d.findLoopBinary()).toBe(BIN);
    expect(execFileSync).not.toHaveBeenCalled();
  });

  it("falls back to which on PATH", () => {
    const { d, execFileSync } = setup({ platform: "linux" });
    execFileSync.mockReturnValue("/usr/bin/loop\n");
    expect(d.findLoopBinary()).toBe("/usr/bin/loop");
    expect(execFileSync).toHaveBeenCalledWith("which", ["loop"], { encoding: "utf-8" });
  });

  it("takes the first line of where on Windows", () => {
    const { d, execFileSync } = setup({ platform: "win32" });
    execFileSync.mockReturnValue("C:\\a\\loop.exe\r\nC:\\b\\loop.exe\r\n");
    expect(d.findLoopBinary()).toBe("C:\\a\\loop.exe");
    expect(execFileSync).toHaveBeenCalledWith("where", ["loop.exe"], { encoding: "utf-8" });
  });

  it("gives null for an empty answer", () => {
    const { d, execFileSync } = setup();
    execFileSync.mockReturnValue("  ");
    expect(d.findLoopBinary()).toBeNull();
  });

  it("gives null when which fails", () => {
    const { d, execFileSync } = setup();
    execFileSync.mockImplementation(() => {
      throw new Error("not found");
    });
    expect(d.findLoopBinary()).toBeNull();
  });
});

describe("isDaemonRunning", () => {
  it("asks the health endpoint", async () => {
    const { d, fetch } = setup();
    expect(await d.isDaemonRunning()).toBe(true);
    expect(fetch).toHaveBeenCalledWith("http://api/api/health", { signal: expect.any(AbortSignal) });
  });

  it("is false on an error status", async () => {
    const { d, fetch } = setup();
    fetch.mockResolvedValue(new Response(null, { status: 503 }));
    expect(await d.isDaemonRunning()).toBe(false);
  });

  it("is false when the request fails", async () => {
    const { d, fetch } = setup();
    fetch.mockRejectedValue(new Error("refused"));
    expect(await d.isDaemonRunning()).toBe(false);
  });
});

describe("ensureDaemon", () => {
  it("onboards when there's no config, then restarts and waits until healthy", async () => {
    const { d, execFileSync, fetch, sleep } = setup({ files: { [BIN]: "" }, hasConfig: false });
    fetch.mockResolvedValueOnce(new Response(null, { status: 503 })).mockResolvedValue(new Response(null, { status: 200 }));
    await d.ensureDaemon();
    expect(execFileSync.mock.calls).toEqual([
      [BIN, ["onboard:global"], { encoding: "utf-8", timeout: 10_000 }],
      [BIN, ["daemon:restart"], { encoding: "utf-8", timeout: 30_000 }],
    ]);
    expect(sleep).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledWith(500);
    expect(log).toHaveBeenCalledWith("Created ~/.loop/config.json");
    expect(log).toHaveBeenCalledWith("Loop daemon is ready");
  });

  it("goes on when onboarding and the restart fail, and gives up after 30 tries", async () => {
    const { d, execFileSync, fetch, sleep } = setup({ files: { [BIN]: "" }, hasConfig: false });
    execFileSync.mockImplementation(() => {
      throw new Error("boom");
    });
    fetch.mockRejectedValue(new Error("refused"));
    await d.ensureDaemon();
    expect(warn).toHaveBeenCalledWith("onboard:global failed:", expect.any(Error));
    expect(warn).toHaveBeenCalledWith("daemon:restart failed:", expect.any(Error));
    expect(warn).toHaveBeenCalledWith("Loop daemon did not become healthy within 15s");
    expect(sleep).toHaveBeenCalledTimes(30);
  });

  it("does nothing without a binary", async () => {
    const { d, execFileSync, sleep } = setup({ hasConfig: false });
    execFileSync.mockImplementation(() => {
      throw new Error("not found");
    });
    await d.ensureDaemon();
    expect(execFileSync.mock.calls.map((c) => c[0])).toEqual(["which", "which"]);
    expect(sleep).not.toHaveBeenCalled();
  });

  it.each([
    [true, "Loop daemon is already running"],
    [false, "Loop daemon not running — start it manually with 'loop serve' on Windows"],
  ])("only checks the daemon on Windows (running=%s)", async (running, msg) => {
    const { d, execFileSync, fetch } = setup({ platform: "win32", files: { "/res/bin/loop.exe": "" } });
    fetch.mockResolvedValue(new Response(null, { status: running ? 200 : 503 }));
    await d.ensureDaemon();
    expect(execFileSync).not.toHaveBeenCalled();
    expect(log).toHaveBeenCalledWith(msg);
  });
});

describe("installCLI", () => {
  const LINK = "/usr/local/bin/loop";
  const WIN_LINK = "/la/Loop/bin/loop.cmd";
  const winScript = '@echo off\n"/res/bin/loop.exe" %*\n';

  it.each([false, true])("reports a missing binary only when interactive=%s", (interactive) => {
    const { d, dialog } = setup();
    d.installCLI(interactive);
    expect(dialog.showMessageBox).toHaveBeenCalledTimes(interactive ? 1 : 0);
    if (interactive) expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "CLI binary not found.", detail: "Not available in dev mode.", buttons: ["OK"] });
  });

  it.each([false, true])("leaves a correct symlink alone (interactive=%s)", (interactive) => {
    const { d, fs, dialog } = setup({ files: { [BIN]: "" }, links: { [LINK]: BIN } });
    d.installCLI(interactive);
    expect(fs.symlinkSync).not.toHaveBeenCalled();
    expect(dialog.showMessageBox).toHaveBeenCalledTimes(interactive ? 1 : 0);
    if (interactive) expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "CLI already installed.", detail: LINK, buttons: ["OK"] });
  });

  it.each([false, true])("replaces a stale symlink (interactive=%s)", (interactive) => {
    const { d, links, fs, dialog } = setup({ files: { [BIN]: "" }, links: { [LINK]: "/old/loop" } });
    d.installCLI(interactive);
    expect(fs.unlinkSync).toHaveBeenCalledWith(LINK);
    expect(links[LINK]).toBe(BIN);
    expect(dialog.showMessageBox).toHaveBeenCalledTimes(interactive ? 1 : 0);
    if (interactive) expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "CLI installed.", detail: `${LINK} -> ${BIN}`, buttons: ["OK"] });
  });

  it("creates a symlink when there is none", () => {
    const { d, links } = setup({ files: { [BIN]: "" } });
    d.installCLI();
    expect(links[LINK]).toBe(BIN);
  });

  it.each([false, true])("leaves a correct Windows script alone (interactive=%s)", (interactive) => {
    const { d, fs, dialog } = setup({ platform: "win32", env: { LOCALAPPDATA: "/la" }, files: { "/res/bin/loop.exe": "", [WIN_LINK]: winScript } });
    d.installCLI(interactive);
    expect(fs.writeFileSync).not.toHaveBeenCalled();
    expect(dialog.showMessageBox).toHaveBeenCalledTimes(interactive ? 1 : 0);
  });

  it("writes the Windows script when it differs", () => {
    const { d, fs, files } = setup({ platform: "win32", env: { LOCALAPPDATA: "/la" }, files: { "/res/bin/loop.exe": "", [WIN_LINK]: "old" } });
    d.installCLI();
    expect(fs.mkdirSync).toHaveBeenCalledWith("/la/Loop/bin", { recursive: true });
    expect(files[WIN_LINK]).toBe(winScript);
  });

  it("writes the Windows script under a relative path without LOCALAPPDATA", () => {
    const { d, files } = setup({ platform: "win32", files: { "/res/bin/loop.exe": "" } });
    d.installCLI();
    expect(files["Loop/bin/loop.cmd"]).toBe(winScript);
  });

  describe("without write access", () => {
    function denied(platform: NodeJS.Platform) {
      const s = setup({ platform, files: { [BIN]: "" } });
      s.fs.symlinkSync.mockImplementation(() => {
        throw new Error("EACCES");
      });
      s.fs.writeFileSync.mockImplementation(() => {
        throw new Error("EACCES");
      });
      return s;
    }

    it.each([false, true])("asks for admin rights with osascript on macOS (interactive=%s)", (interactive) => {
      const { d, execFileSync, dialog } = denied("darwin");
      d.installCLI(interactive);
      expect(execFileSync).toHaveBeenCalledWith("osascript", ["-e", `do shell script "ln -sf '${BIN}' '${LINK}'" with administrator privileges`], { timeout: 60_000 });
      expect(dialog.showMessageBox).toHaveBeenCalledTimes(interactive ? 1 : 0);
      if (interactive) expect(dialog.showMessageBox).toHaveBeenCalledWith({ message: "CLI installed.", detail: `${LINK} -> ${BIN}`, buttons: ["OK"] });
    });

    it("asks for admin rights with pkexec on Linux", () => {
      const { d, execFileSync } = denied("linux");
      d.installCLI();
      expect(execFileSync).toHaveBeenCalledWith("pkexec", ["ln", "-sf", BIN, LINK], { timeout: 60_000 });
    });

    it("runs nothing elevated on Windows", () => {
      const s = setup({ platform: "win32", files: { "/res/bin/loop.exe": "" } });
      s.fs.writeFileSync.mockImplementation(() => {
        throw new Error("EACCES");
      });
      s.d.installCLI(true);
      expect(s.execFileSync).not.toHaveBeenCalled();
      expect(s.dialog.showMessageBox).toHaveBeenCalledWith(expect.objectContaining({ message: "CLI installed." }));
    });

    it.each([false, true])("tells how to install by hand when that fails too (interactive=%s)", (interactive) => {
      const { d, execFileSync, dialog } = denied("darwin");
      execFileSync.mockImplementation(() => {
        throw new Error("cancelled");
      });
      d.installCLI(interactive);
      expect(dialog.showMessageBox).toHaveBeenCalledTimes(interactive ? 1 : 0);
      if (interactive) expect(dialog.showMessageBox).toHaveBeenCalledWith({ type: "error", message: "Could not install CLI.", detail: `Run manually:\nsudo ln -sf "${BIN}" ${LINK}`, buttons: ["OK"] });
    });
  });
});
