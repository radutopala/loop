import { describe, expect, it } from "vitest";
import { pointerLeft } from "./useRowHover";

describe("pointerLeft", () => {
  const inside = {} as EventTarget;
  const outside = {} as EventTarget;
  const row = { contains: (n: Node | null) => n === (inside as unknown as Node) };

  it.each([
    { name: "a target in the row stays", row, target: inside, want: false },
    { name: "a target outside the row leaves", row, target: outside, want: true },
    { name: "no target leaves", row, target: null, want: true },
    { name: "a row that's gone leaves", row: null, target: inside, want: true },
  ])("$name", ({ row, target, want }) => {
    expect(pointerLeft(row, target)).toBe(want);
  });
});
