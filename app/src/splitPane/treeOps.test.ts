import { describe, expect, it } from "vitest";
import { flexPercents } from "./treeOps";

describe("flexPercents", () => {
  it.each([
    { flexes: [1, 1], want: [50, 50] },
    { flexes: [0.6, 0.4], want: [60, 40] },
    { flexes: [1, 1, 1], want: [33, 33, 33] },
    { flexes: [1.25, 0.75], want: [63, 38] },
    { flexes: [0.5, 0, 0.5], want: [50, 0, 50] },
    { flexes: [0, 0], want: [0, 0] },
    { flexes: [], want: [] },
  ])("$flexes → $want", ({ flexes, want }) => {
    expect(flexPercents(flexes)).toEqual(want);
  });
});
