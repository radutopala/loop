import { describe, expect, it } from "vitest";
import type { RootEntry } from "../../api/loopApi";
import { fileSearchLabel, nextSearchIndex } from "./fileSearch";

const roots: RootEntry[] = [
  { index: 0, path: "/w/app", display_path: "~/w/app", name: "app" },
  { index: 1, path: "/w/lib", display_path: "~/w/lib", name: "lib" },
];

describe("fileSearchLabel", () => {
  it.each([
    ["a nested file, one root", { root_index: 0, rel_path: "src/a.ts", name: "a.ts" }, roots.slice(0, 1), { name: "a.ts", where: "src" }],
    ["a top-level file, one root", { root_index: 0, rel_path: "a.ts", name: "a.ts" }, roots.slice(0, 1), { name: "a.ts", where: "" }],
    ["a nested file, two roots", { root_index: 1, rel_path: "x/b.go", name: "b.go" }, roots, { name: "b.go", where: "lib/x" }],
    ["a top-level file, two roots", { root_index: 1, rel_path: "b.go", name: "b.go" }, roots, { name: "b.go", where: "lib" }],
  ])("%s", (_name, result, rs, want) => {
    expect(fileSearchLabel(result, rs)).toEqual(want);
  });
});

describe("nextSearchIndex", () => {
  it.each([
    ["down", "ArrowDown", 0, 3, 1],
    ["down wraps", "ArrowDown", 2, 3, 0],
    ["up wraps", "ArrowUp", 0, 3, 2],
    ["another key", "a", 1, 3, null],
    ["no results", "ArrowDown", 0, 0, null],
  ])("%s", (_name, key, index, count, want) => {
    expect(nextSearchIndex(key, index, count)).toBe(want);
  });
});
