import { describe, expect, it } from "vitest";
import { moveTracker } from "./useRowHover";

describe("moveTracker", () => {
  it.each([
    { name: "a move the row saw is inside", marks: [true], want: [false] },
    { name: "a move the row didn't see is outside", marks: [false], want: [true] },
    { name: "each mark covers only its own move", marks: [true, false, true], want: [false, true, false] },
  ])("$name", ({ marks, want }) => {
    const moves = moveTracker();
    const got = marks.map((seen) => {
      if (seen) moves.markInside();
      return moves.outside();
    });
    expect(got).toEqual(want);
  });
});
