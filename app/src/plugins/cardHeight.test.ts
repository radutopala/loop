import { describe, expect, it } from "vitest";
import { componentMaxHeight, componentMinHeight } from "../components/chat/componentFence";
import { cardHeight } from "./cardHeight";

describe("cardHeight", () => {
  it.each([
    { name: "within bounds, rounded up", h: 240.2, want: 241 },
    { name: "too short", h: 1, want: componentMinHeight },
    { name: "too tall", h: 5000, want: componentMaxHeight },
  ])("$name", ({ h, want }) => {
    expect(cardHeight(h)).toBe(want);
  });
});
