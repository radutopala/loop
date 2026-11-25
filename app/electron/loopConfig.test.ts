import { describe, expect, it } from "vitest";
import { loopConfig, stripHJSON } from "./loopConfig";

const configPath = "/home/u/.loop/config.json";
const tokenPath = "/data/loop/api-token";

/** A loopConfig over an in-memory file set. */
function setup(files: Record<string, string>, env: NodeJS.ProcessEnv = {}) {
  const fs = {
    readFileSync: (p: string) => {
      if (!(p in files)) throw new Error(`ENOENT: ${p}`);
      return files[p]!;
    },
    existsSync: (p: string) => p in files,
  };
  return loopConfig({ fs: fs as never, homedir: () => "/home/u", appData: () => "/data", env });
}

describe("stripHJSON", () => {
  it.each([
    ["line comments", '{\n  // note\n  "a": 1 // trailing\n}', '{\n  \n  "a": 1 \n}'],
    ["block comments", '{ /* x */ "a": 1 }', '{  "a": 1 }'],
    ["an unterminated block comment", '{"a": 1} /* open', '{"a": 1} '],
    ["trailing commas", '{"a": [1, 2, ], }', '{"a": [1, 2]}'],
    ["slashes inside strings", '{"u": "http://x/*y*/"}', '{"u": "http://x/*y*/"}'],
    ["escaped quotes inside strings", '{"s": "a\\"//b"}', '{"s": "a\\"//b"}'],
    ["a lone slash", '{"a": 4 / 2}', '{"a": 4 / 2}'],
  ])("handles %s", (_name, input, want) => {
    expect(stripHJSON(input)).toBe(want);
  });
});

describe("loopConfig", () => {
  it("reads the API token trimmed and builds the auth header", () => {
    const c = setup({ [tokenPath]: " tok \n" });
    expect(c.readApiToken()).toBe("tok");
    expect(c.authHeaders()).toEqual({ Authorization: "Bearer tok" });
  });

  it("gives no token and no header before the daemon wrote one", () => {
    const c = setup({});
    expect(c.readApiToken()).toBe("");
    expect(c.authHeaders()).toEqual({});
  });

  it("tells whether the config file exists", () => {
    expect(setup({ [configPath]: "{}" }).hasLoopConfig()).toBe(true);
    expect(setup({}).hasLoopConfig()).toBe(false);
  });

  it.each([
    ["LOOP_API_URL wins", { [configPath]: '{"api_addr": ":9000"}' }, { LOOP_API_URL: "http://env:1" }, "http://env:1"],
    ["a port-only address goes to localhost", { [configPath]: '{\n  // port\n  "api_addr": ":9000",\n}' }, {}, "http://localhost:9000"],
    ["a host address is kept", { [configPath]: '{"api_addr": "10.0.0.2:8000"}' }, {}, "http://10.0.0.2:8000"],
    ["no address defaults to 8222", { [configPath]: "{}" }, {}, "http://localhost:8222"],
    ["no config defaults to 8222", {}, {}, "http://localhost:8222"],
    ["a broken config defaults to 8222", { [configPath]: "{nope" }, {}, "http://localhost:8222"],
  ])("resolveApiUrl: %s", (_name, files, env, want) => {
    expect(setup(files, env).resolveApiUrl()).toBe(want);
  });

  it.each([
    ["the desktop section", '{"desktop": {"theme": "light"}}', { theme: "light" }],
    ["no desktop section", '{"api_addr": ":1"}', {}],
    ["a desktop value that isn't an object", '{"desktop": "x"}', {}],
    ["a broken config", "{nope", {}],
  ])("readDesktopConfig: %s", (_name, text, want) => {
    expect(setup({ [configPath]: text }).readDesktopConfig()).toEqual(want);
  });
});
