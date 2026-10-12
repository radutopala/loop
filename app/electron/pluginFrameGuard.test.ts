import { describe, expect, it } from "vitest";
import { allowFrameNavigation, pluginFrameBase } from "./pluginFrameGuard";

function cap(payload: object): string {
  const p = btoa(JSON.stringify(payload)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return `${p}.c2ln`;
}

const plugin = cap({ k: "plugin", s: "echo", e: 9999999999 });
const playground = cap({ k: "playground", c: "ch", s: "demo", e: 9999999999 });
const base = `http://127.0.0.1:8222/c/${plugin}/`;

describe("pluginFrameBase", () => {
  it.each([
    ["a plugin page", `${base}ui/quiz/index.html`, base],
    ["a plugin link's root", base, base],
    ["a playground page", `http://127.0.0.1:8222/c/${playground}/index.html`, ""],
    ["the app", "http://localhost:5173/#ch", ""],
    ["a file url", "file:///Applications/Loop.app/index.html", ""],
    ["not a url", "nope", ""],
    ["a cap that isn't base64 JSON", "http://127.0.0.1:8222/c/%%%.sig/x", ""],
    ["a cap without a path after it", `http://127.0.0.1:8222/c/${plugin}`, ""],
  ])("%s", (_name, url, want) => {
    expect(pluginFrameBase(url)).toBe(want);
  });
});

describe("allowFrameNavigation", () => {
  it.each([
    ["a plugin page to another page of its bundle", `${base}ui/quiz/index.html`, `${base}ui/quiz/history.html`, true],
    ["a plugin page to an outside site", `${base}ui/quiz/index.html`, "https://example.com/?d=secret", false],
    ["a plugin page to another link of the daemon", `${base}ui/quiz/index.html`, `http://127.0.0.1:8222/c/${playground}/x`, false],
    ["a plugin page to the API", `${base}ui/quiz/index.html`, "http://127.0.0.1:8222/api/channels", false],
    ["a plugin page to a data url", `${base}ui/quiz/index.html`, "data:text/html,hi", false],
    ["a plugin page to the same path on another host", `${base}ui/quiz/index.html`, `http://evil.test:8222/c/${plugin}/x`, false],
    ["a playground page to an outside site", `http://127.0.0.1:8222/c/${playground}/index.html`, "https://example.com/", true],
    ["a blank frame", "about:blank", "https://example.com/", true],
  ])("%s", (_name, from, to, want) => {
    expect(allowFrameNavigation(from, to)).toBe(want);
  });
});
