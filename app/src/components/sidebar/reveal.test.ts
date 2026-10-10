import { describe, expect, it } from "vitest";
import type { Channel } from "../../types";
import { hasDescendant } from "./reveal";

const ch = (id: string) => ({ id }) as Channel;

describe("hasDescendant", () => {
  const byParent = { t1: [ch("t1a")], t1a: [ch("t1a1")] };

  it.each([
    ["a thread", "t2", true],
    ["a sub-thread", "t1a", true],
    ["a deeper one", "t1a1", true],
    ["anything else", "other", false],
  ])("finds %s", (_, id, want) => {
    expect(hasDescendant([ch("t1"), ch("t2")], byParent, id)).toBe(want);
  });

  it("works without sub-threads and ends on a cycle", () => {
    expect(hasDescendant([ch("t1")], undefined, "t1a")).toBe(false);
    expect(hasDescendant([ch("a")], { a: [ch("b")], b: [ch("a")] }, "c")).toBe(false);
  });
});
