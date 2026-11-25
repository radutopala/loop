import { afterEach, describe, expect, it, vi } from "vitest";
import { logErr } from "./log";

describe("logErr", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("warns with the context and the error", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const err = new Error("boom");
    logErr("saving file")(err);
    expect(warn).toHaveBeenCalledWith("[loop] saving file:", err);
  });
});
