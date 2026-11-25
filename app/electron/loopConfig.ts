import type fsType from "node:fs";
import path from "node:path";

/** Strip HJSON comments (// and /* *​/) and trailing commas to get valid JSON. */
export function stripHJSON(text: string): string {
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

interface LoopConfigFile {
  api_addr?: string;
}

export interface LoopConfigDeps {
  fs: Pick<typeof fsType, "readFileSync" | "existsSync">;
  /** The user's home directory, read on each call. */
  homedir: () => string;
  /** Electron's appData directory, read on each call. */
  appData: () => string;
  env: NodeJS.ProcessEnv;
}

export interface LoopConfig {
  readApiToken(): string;
  authHeaders(): Record<string, string>;
  hasLoopConfig(): boolean;
  resolveApiUrl(): string;
  readDesktopConfig(): Record<string, unknown>;
}

/** Reads ~/.loop/config.json and the owner API token the daemon writes. */
export function loopConfig({ fs, homedir, appData, env }: LoopConfigDeps): LoopConfig {
  function loopDir(): string {
    return path.join(homedir(), ".loop");
  }

  /** The owner API token file the daemon writes (Go's os.UserConfigDir()/loop,
   *  which is Electron's appData on every platform). */
  function apiTokenPath(): string {
    return path.join(appData(), "loop", "api-token");
  }

  /** Read the owner API token fresh, so a rotation is picked up. "" before the
   *  daemon has written it. */
  function readApiToken(): string {
    try {
      return fs.readFileSync(apiTokenPath(), "utf-8").trim();
    } catch {
      return "";
    }
  }

  function authHeaders(): Record<string, string> {
    const tok = readApiToken();
    return tok ? { Authorization: `Bearer ${tok}` } : {};
  }

  function loopConfigPath(): string {
    return path.join(loopDir(), "config.json");
  }

  function readLoopConfig(): LoopConfigFile {
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
    if (env.LOOP_API_URL) return env.LOOP_API_URL;
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
    } catch {
      /* ignore */
    }
    return {};
  }

  return { readApiToken, authHeaders, hasLoopConfig, resolveApiUrl, readDesktopConfig };
}
