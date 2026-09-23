import { describe, expect, it } from "vitest";
import { placeMenu } from "./menuPlacement";

// anchor builds a 10x10 button rect at (left, top).
function anchor(left: number, top: number): DOMRect {
  return { left, top, right: left + 10, bottom: top + 10, width: 10, height: 10, x: left, y: top, toJSON: () => ({}) };
}

const viewport = { width: 1000, height: 800 };

describe("placeMenu", () => {
  it("opens below the anchor when it fits", () => {
    expect(placeMenu(anchor(100, 20), { width: 200, height: 300 }, viewport)).toEqual({ top: 32, left: 100 });
  });

  it("flips above the anchor when there's no room below", () => {
    expect(placeMenu(anchor(100, 600), { width: 200, height: 300 }, viewport)).toEqual({ top: 298, left: 100 });
  });

  it("pins to the bottom edge when it fits neither below nor above", () => {
    expect(placeMenu(anchor(100, 400), { width: 200, height: 500 }, viewport)).toEqual({ top: 292, left: 100 });
  });

  it("scrolls when taller than the window", () => {
    expect(placeMenu(anchor(100, 400), { width: 200, height: 900 }, viewport)).toEqual({ top: 8, left: 100, maxHeight: 784 });
  });

  it("shifts left to stay inside the right edge", () => {
    expect(placeMenu(anchor(900, 20), { width: 200, height: 100 }, viewport)).toEqual({ top: 32, left: 792 });
  });

  it("never goes past the left margin", () => {
    expect(placeMenu(anchor(100, 20), { width: 1200, height: 100 }, viewport)).toEqual({ top: 32, left: 8 });
  });
});
