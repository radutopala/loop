import { describe, expect, it } from "vitest";
import { classifyDiffLines } from "./diffLines";

describe("classifyDiffLines", () => {
  it("classifies each unified diff line", () => {
    const diff = "--- a/config\n+++ b/config\n@@ -1,2 +1,3 @@\n [core]\n-\tbare = false\n+\tfsmonitor = sh x\n";
    expect(classifyDiffLines(diff)).toEqual([
      { kind: "meta", text: "--- a/config" },
      { kind: "meta", text: "+++ b/config" },
      { kind: "hunk", text: "@@ -1,2 +1,3 @@" },
      { kind: "context", text: " [core]" },
      { kind: "del", text: "-\tbare = false" },
      { kind: "add", text: "+\tfsmonitor = sh x" },
    ]);
  });

  it("returns nothing for an empty diff", () => {
    expect(classifyDiffLines("")).toEqual([]);
  });

  it("keeps a last line with no trailing newline", () => {
    expect(classifyDiffLines("+x")).toEqual([{ kind: "add", text: "+x" }]);
  });
});
