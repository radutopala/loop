import { describe, expect, it } from "vitest";
import { clampSplit } from "./LearnSplit";

describe("clampSplit", () => {
  it.each([
    { split: 0.5, want: 0.5 },
    { split: 0.3, want: 0.3 },
    { split: 0.15, want: 0.15 },
    { split: 0.05, want: 0.15 },
    { split: -1, want: 0.15 },
    { split: 0.95, want: 0.85 },
    { split: 2, want: 0.85 },
  ])("keeps $split at $want", ({ split, want }) => {
    expect(clampSplit(split)).toBeCloseTo(want);
  });
});
