import { describe, expect, it } from "vitest";
import { allowPermissionCheck, allowPermissionRequest, isAppEntryURL } from "./mediaPermission";

const dev = "http://localhost:5173";
const packaged = "file:///Applications/Loop.app/Contents/Resources/app.asar/dist/index.html";
const pluginFrame = "http://127.0.0.1:8222/c/eyJrIjoicGx1Z2luIn0.c2ln/ui/index.html";

describe("isAppEntryURL", () => {
  it.each([
    ["the dev page", "http://localhost:5173/", dev, true],
    ["the dev page with a channel hash", "http://localhost:5173/#ch-1", dev, true],
    ["another dev path", "http://localhost:5173/other.html", dev, false],
    ["another port", "http://localhost:5174/", dev, false],
    ["the packaged page with a hash", `${packaged}#ch`, packaged, true],
    ["another file", "file:///tmp/index.html", packaged, false],
    ["not a url", "nope", dev, false],
  ])("%s", (_name, url, entry, want) => {
    expect(isAppEntryURL(url, entry)).toBe(want);
  });
});

describe("allowPermissionRequest", () => {
  const app = { isMainFrame: true, requestingUrl: `${packaged}#ch` };
  it.each([
    ["the microphone for the packaged page", app, packaged, true],
    ["the microphone for the dev page", { isMainFrame: true, requestingUrl: "http://localhost:5173/#x" }, dev, true],
    ["the camera", { ...app, mediaTypes: ["video"] }, packaged, false],
    ["camera and microphone", { ...app, mediaTypes: ["audio", "video"] }, packaged, false],
    ["no media type (screen capture and the like)", { ...app, mediaTypes: [] }, packaged, false],
    ["the microphone for a subframe of the app", { ...app, isMainFrame: false }, packaged, false],
    ["the microphone for a plugin frame", { isMainFrame: false, requestingUrl: pluginFrame }, packaged, false],
    ["the microphone for another page in the main frame", { isMainFrame: true, requestingUrl: "https://example.com/" }, packaged, false],
    ["the microphone with no URL", { isMainFrame: true }, packaged, false],
  ] as const)("media: %s", (_name, info, entry, want) => {
    const withTypes = "mediaTypes" in info ? info : { ...info, mediaTypes: ["audio"] };
    expect(allowPermissionRequest("media", { ...withTypes, mediaTypes: [...withTypes.mediaTypes] }, entry)).toBe(want);
  });

  it.each(["notifications", "clipboard-read", "clipboard-sanitized-write", "fullscreen", "openExternal"])("keeps granting %s anywhere", (perm) => {
    expect(allowPermissionRequest(perm, { isMainFrame: false, requestingUrl: pluginFrame }, packaged)).toBe(true);
  });
});

describe("allowPermissionCheck", () => {
  it.each([
    ["audio for the app", { isMainFrame: true, requestingUrl: `${dev}/#c`, mediaType: "audio" }, true],
    ["video for the app", { isMainFrame: true, requestingUrl: `${dev}/`, mediaType: "video" }, false],
    ["an unknown media type", { isMainFrame: true, requestingUrl: `${dev}/`, mediaType: "unknown" }, false],
    ["audio for a plugin frame", { isMainFrame: false, requestingUrl: pluginFrame, mediaType: "audio" }, false],
    ["audio for another page", { isMainFrame: true, requestingUrl: "https://example.com/", mediaType: "audio" }, false],
  ])("media: %s", (_name, info, want) => {
    expect(allowPermissionCheck("media", info, dev)).toBe(want);
  });

  it("keeps passing other checks", () => {
    expect(allowPermissionCheck("clipboard-read", { isMainFrame: false, requestingUrl: pluginFrame }, dev)).toBe(true);
  });
});
