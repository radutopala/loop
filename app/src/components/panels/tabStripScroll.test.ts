import { describe, expect, it } from "vitest";
import { scrollLeftToShow } from "./tabStripScroll";

describe("scrollLeftToShow", () => {
  const strip = { left: 100, width: 300, scrollLeft: 50 };
  it.each([
    ["in view", { left: 150, width: 80 }, null],
    ["touching both edges", { left: 100, width: 300 }, null],
    ["past the right edge", { left: 380, width: 80 }, 50 + 60],
    ["before the left edge", { left: 70, width: 80 }, 50 - 30],
    ["wider than the strip", { left: 200, width: 400 }, 50 + 100],
  ])("an item %s", (_, item, want) => {
    expect(scrollLeftToShow(strip, item)).toBe(want);
  });
});
