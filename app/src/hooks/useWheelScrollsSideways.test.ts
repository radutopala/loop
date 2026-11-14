import { describe, expect, it } from "vitest";
import { sidewaysWheelDelta } from "./useWheelScrollsSideways";

describe("sidewaysWheelDelta", () => {
  it.each<[string, number, number, number | null]>([
    ["a mouse wheel scrolls the strip", 0, 40, 40],
    ["upwards scrolls back", 0, -40, -40],
    ["a mostly vertical swipe still counts", 5, 40, 40],
    ["a sideways swipe is left to the browser", 30, 10, null],
    ["an even one too", 20, 20, null],
    ["no vertical motion does nothing", 0, 0, null],
  ])("%s", (_name, dx, dy, want) => {
    expect(sidewaysWheelDelta(dx, dy)).toBe(want);
  });
});
