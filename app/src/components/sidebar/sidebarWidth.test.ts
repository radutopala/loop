import { describe, expect, it } from "vitest";
import { SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MIN_WIDTH, savedSidebarWidth } from "./sidebarWidth";

describe("savedSidebarWidth", () => {
  it.each([
    ["nothing saved", null, SIDEBAR_DEFAULT_WIDTH],
    ["a saved width", 320, 320],
    ["narrower than the minimum", 40, SIDEBAR_MIN_WIDTH],
    ["not a number", "320", SIDEBAR_DEFAULT_WIDTH],
    ["NaN", Number.NaN, SIDEBAR_DEFAULT_WIDTH],
    ["Infinity", Number.POSITIVE_INFINITY, SIDEBAR_DEFAULT_WIDTH],
  ])("%s", (_name, saved, want) => {
    expect(savedSidebarWidth(saved)).toBe(want);
  });
});
