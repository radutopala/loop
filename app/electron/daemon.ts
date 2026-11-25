import type { ExecFileSyncOptions } from "node:child_process";
import type fsType from "node:fs";
import path from "node:path";
import type { LoopConfig } from "./loopConfig";

export type ExecFileSync = (file: string, args: readonly string[], options?: ExecFileSyncOptions) => string | Buffer;

export interface DaemonDeps {
  fs: Pick<typeof fsType, "existsSync" | "readFileSync" | "readlinkSync" | "unlinkSync" | "symlinkSync" | "mkdirSync" | "writeFileSync">;
  execFileSync: ExecFileSync;
  fetch: typeof fetch;
  dialog: Pick<Electron.Dialog, "showMessageBox">;
  platform: NodeJS.Platform;
  env: NodeJS.ProcessEnv;
  /** Electron's process.resourcesPath. */
  resourcesPath: string;
  config: Pick<LoopConfig, "hasLoopConfig" | "resolveApiUrl">;
  sleep: (ms: number) => Promise<void>;
}

export interface LoopDaemon {
  bundledBinaryPath(): string | null;
  findLoopBinary(): string | null;
  isDaemonRunning(): Promise<boolean>;
  ensureDaemon(): Promise<void>;
  installCLI(interactive?: boolean): void;
}

/** Finds the loop binary and runs the daemon and CLI install through it. */
export function loopDaemon({ fs, execFileSync, fetch, dialog, platform, env, resourcesPath, config, sleep }: DaemonDeps): LoopDaemon {
  // --- Bundled binary resolution ---

  function binaryName(): string {
    return platform === "win32" ? "loop.exe" : "loop";
  }

  function bundledBinaryPath(): string | null {
    // In production: resources/bin/loop (or loop.exe on Windows)
    // process.resourcesPath points to <app>/Contents/Resources (macOS) or <app>/resources (Linux/Windows)
    const name = binaryName();
    const resourcePath = path.join(resourcesPath, "bin", name);
    if (fs.existsSync(resourcePath)) return resourcePath;
    // Fallback: old flat layout (resources/loop)
    const flatPath = path.join(resourcesPath, name);
    if (fs.existsSync(flatPath)) return flatPath;
    return null;
  }

  function findLoopBinary(): string | null {
    // 1. Bundled binary
    const bundled = bundledBinaryPath();
    if (bundled) return bundled;

    // 2. On PATH (for dev mode / system install)
    try {
      const whichCmd = platform === "win32" ? "where" : "which";
      const result = (execFileSync(whichCmd, [binaryName()], { encoding: "utf-8" }) as string).trim();
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
      const resp = await fetch(`${config.resolveApiUrl()}/api/health`, {
        signal: AbortSignal.timeout(2000),
      });
      return resp.ok;
    } catch {
      return false;
    }
  }

  function ensureLoopConfig(): void {
    if (config.hasLoopConfig()) return;

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
    if (platform === "win32") {
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
      await sleep(500);
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

    const linkPath = platform === "win32" ? path.join(env.LOCALAPPDATA || "", "Loop", "bin", "loop.cmd") : "/usr/local/bin/loop";

    // Check if already installed correctly.
    try {
      if (platform === "win32") {
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
    } catch {
      /* not installed yet */
    }

    try {
      if (platform === "win32") {
        const binDir = path.dirname(linkPath);
        fs.mkdirSync(binDir, { recursive: true });
        fs.writeFileSync(linkPath, `@echo off\n"${bundled}" %*\n`);
      } else {
        // Try direct symlink first.
        try {
          fs.unlinkSync(linkPath);
        } catch {
          /* ignore */
        }
        fs.symlinkSync(bundled, linkPath);
      }
      if (interactive) dialog.showMessageBox({ message: "CLI installed.", detail: `${linkPath} -> ${bundled}`, buttons: ["OK"] });
    } catch {
      // Needs elevated permissions.
      try {
        if (platform === "darwin") {
          execFileSync("osascript", ["-e", `do shell script "ln -sf '${bundled}' '${linkPath}'" with administrator privileges`], { timeout: 60_000 });
        } else if (platform !== "win32") {
          execFileSync("pkexec", ["ln", "-sf", bundled, linkPath], { timeout: 60_000 });
        }
        if (interactive) dialog.showMessageBox({ message: "CLI installed.", detail: `${linkPath} -> ${bundled}`, buttons: ["OK"] });
      } catch {
        if (interactive) dialog.showMessageBox({ type: "error", message: "Could not install CLI.", detail: `Run manually:\nsudo ln -sf "${bundled}" ${linkPath}`, buttons: ["OK"] });
      }
    }
  }

  return { bundledBinaryPath, findLoopBinary, isDaemonRunning, ensureDaemon, installCLI };
}
