// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { openExternalUrl } from "./openExternal";

describe("openExternalUrl", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("uses the Electron bridge when it has openExternal", () => {
    const openExternal = vi.fn(async () => {});
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    vi.stubGlobal("loopAPI", { openExternal });
    openExternalUrl("https://example.test/");
    expect(openExternal).toHaveBeenCalledWith("https://example.test/");
    expect(open).not.toHaveBeenCalled();
  });

  it("falls back to window.open without the bridge", () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    openExternalUrl("https://example.test/");
    expect(open).toHaveBeenCalledWith("https://example.test/", "_blank", "noopener,noreferrer");
  });
});
