import { describe, expect, it } from "vitest";
import { selectAfterDelete } from "./selectAfterDelete";

const channels = [
  { id: "chan", parent_id: "" },
  { id: "thread", parent_id: "chan" },
  { id: "wt", parent_id: "chan" },
  { id: "wt-thread", parent_id: "wt" },
  { id: "orphan", parent_id: "gone" },
];

describe("selectAfterDelete", () => {
  it.each<[string, string | null, string[], string | null | undefined]>([
    ["nothing selected", null, ["thread"], undefined],
    ["another row deleted", "chan", ["thread"], undefined],
    ["the selected thread", "thread", ["thread"], "chan"],
    ["the selected worktree thread", "wt", ["wt"], "chan"],
    ["a thread of a worktree", "wt-thread", ["wt-thread"], "wt"],
    ["a thread and its worktree", "wt-thread", ["wt-thread", "wt"], "chan"],
    ["a thread and its channel", "thread", ["thread", "chan"], null],
    ["the selected channel", "chan", ["chan"], null],
    ["a thread whose parent isn't listed", "orphan", ["orphan"], null],
    ["a row not listed", "unknown", ["unknown"], null],
  ])("%s", (_name, selected, deleted, want) => {
    expect(selectAfterDelete(selected, deleted, channels)).toBe(want);
  });

  it("stops on a parent cycle", () => {
    const cyclic = [
      { id: "a", parent_id: "b" },
      { id: "b", parent_id: "a" },
    ];
    expect(selectAfterDelete("a", ["a", "b"], cyclic)).toBeNull();
  });
});
