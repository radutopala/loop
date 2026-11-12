import { describe, expect, it } from "vitest";
import { renamedPathKey, renameMapKeys, renameSetKeys } from "./renamePath";

describe("renamedPathKey", () => {
  it.each([
    ["the renamed file", "0:a/x.ts", "0:a/x.ts", "0:a/y.ts", "0:a/y.ts"],
    ["a file in a renamed dir", "0:a/x.ts", "0:a", "0:b", "0:b/x.ts"],
    ["a sibling sharing the prefix", "0:ab/x.ts", "0:a", "0:b", "0:ab/x.ts"],
    ["another root", "1:a/x.ts", "0:a", "0:b", "1:a/x.ts"],
  ])("%s", (_name, key, from, to, want) => {
    expect(renamedPathKey(key, from, to)).toBe(want);
  });
});

describe("renameSetKeys", () => {
  it("renames the moved keys", () => {
    expect(renameSetKeys(new Set(["0:a", "0:a/b", "0:c"]), "0:a", "0:z")).toEqual(new Set(["0:z", "0:z/b", "0:c"]));
  });
  it("returns the same set when nothing moves", () => {
    const set = new Set(["0:c"]);
    expect(renameSetKeys(set, "0:a", "0:z")).toBe(set);
  });
});

describe("renameMapKeys", () => {
  it("renames the moved keys in place", () => {
    const map = new Map([
      ["0:a/x", 1],
      ["0:c", 2],
    ]);
    expect(renameMapKeys(map, "0:a", "0:z")).toBe(map);
    expect([...map]).toEqual([
      ["0:c", 2],
      ["0:z/x", 1],
    ]);
  });
});
