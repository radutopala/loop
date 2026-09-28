import { describe, expect, it } from "vitest";
import { composerHeight, composerMaxHeight } from "./composerHeight";

describe("composerHeight", () => {
  // 20px lines + 4px padding: three rows is 64px.
  it("never goes below the minimum rows", () => {
    expect(composerHeight(24, 20, 4, 3, 300)).toEqual({ height: 64, scrolls: false });
  });

  it("grows to fit the content", () => {
    expect(composerHeight(144, 20, 4, 3, 300)).toEqual({ height: 144, scrolls: false });
  });

  it("stops at the maximum and scrolls past it", () => {
    expect(composerHeight(500, 20, 4, 3, 300)).toEqual({ height: 300, scrolls: true });
  });

  it("keeps the minimum when the window is too short for it", () => {
    expect(composerHeight(500, 20, 4, 3, 40)).toEqual({ height: 64, scrolls: true });
  });
});

describe("composerMaxHeight", () => {
  it("is 40% of the window", () => {
    expect(composerMaxHeight(600)).toBe(240);
  });

  it("is capped on tall windows", () => {
    expect(composerMaxHeight(2000)).toBe(360);
  });
});
