import { describe, expect, it } from "vitest";
import { computeSegments, fileKey, formatRenamePath, parseUnifiedDiff } from "./DiffViewer";

const SIMPLE_DIFF = `diff --git a/foo.go b/foo.go
index 111..222 100644
--- a/foo.go
+++ b/foo.go
@@ -1,4 +1,5 @@
 package main
-func old() {}
+func new1() {}
+func new2() {}
 func keep() {}
`;

describe("parseUnifiedDiff", () => {
  it("parses one file with adds, deletes, and context", () => {
    const files = parseUnifiedDiff(SIMPLE_DIFF);
    expect(files).toHaveLength(1);
    expect(files[0]!.path).toBe("foo.go");
    expect(files[0]!.hunks).toHaveLength(1);

    const lines = files[0]!.hunks[0]!.lines;
    expect(lines.map((l) => l.type)).toEqual(["ctx", "del", "add", "add", "ctx"]);
    // New-file numbering: ctx=1, del has no newNum, adds are 2 and 3, ctx=4.
    expect(lines[0]).toMatchObject({ oldNum: 1, newNum: 1 });
    expect(lines[1]).toMatchObject({ type: "del", oldNum: 2, newNum: null });
    expect(lines[2]).toMatchObject({ type: "add", oldNum: null, newNum: 2 });
    expect(lines[3]).toMatchObject({ type: "add", oldNum: null, newNum: 3 });
    expect(lines[4]).toMatchObject({ oldNum: 3, newNum: 4 });
  });

  it("parses multiple files and carries the status through", () => {
    const multi = SIMPLE_DIFF + SIMPLE_DIFF.replace(/foo\.go/g, "bar.go");
    const files = parseUnifiedDiff(multi, "unstaged");
    expect(files.map((f) => f.path)).toEqual(["foo.go", "bar.go"]);
    expect(files.every((f) => f.status === "unstaged")).toBe(true);
  });

  it("parses a new-file diff as all additions", () => {
    const newFile = `diff --git a/fresh.txt b/fresh.txt
new file mode 100644
--- /dev/null
+++ b/fresh.txt
@@ -0,0 +1,2 @@
+line one
+line two
`;
    const files = parseUnifiedDiff(newFile);
    expect(files).toHaveLength(1);
    const lines = files[0]!.hunks[0]!.lines;
    expect(lines.map((l) => l.type)).toEqual(["add", "add"]);
    expect(lines.map((l) => l.newNum)).toEqual([1, 2]);
  });

  it("skips the no-newline marker and returns no files for empty input", () => {
    const withMarker = SIMPLE_DIFF + "\\ No newline at end of file\n";
    expect(parseUnifiedDiff(withMarker)[0]!.hunks[0]!.lines).toHaveLength(5);
    expect(parseUnifiedDiff("")).toEqual([]);
  });
});

describe("computeSegments", () => {
  it("emits top and bottom gaps around a mid-file hunk", () => {
    const diff = `diff --git a/big.go b/big.go
--- a/big.go
+++ b/big.go
@@ -10,3 +10,4 @@
 ctx a
+added
 ctx b
 ctx c
`;
    const parsed = parseUnifiedDiff(diff)[0]!;
    const segments = computeSegments(parsed, 50);
    expect(segments.map((s) => s.kind)).toEqual(["gap", "hunk", "gap"]);
    const top = segments[0]!;
    const bottom = segments[2]!;
    if (top.kind === "gap" && bottom.kind === "gap") {
      expect(top.gap).toMatchObject({ startLine: 1, endLine: 9 });
      expect(bottom.gap.endLine).toBe(50);
    }
  });

  it("emits no gaps for a new file", () => {
    const newFile = `diff --git a/n.txt b/n.txt
--- /dev/null
+++ b/n.txt
@@ -0,0 +1,1 @@
+only line
`;
    const parsed = parseUnifiedDiff(newFile)[0]!;
    expect(computeSegments(parsed, 1).map((s) => s.kind)).toEqual(["hunk"]);
  });
});

describe("formatRenamePath", () => {
  it("collapses the common prefix and suffix", () => {
    expect(formatRenamePath("internal/api/old_handler.go", "internal/api/new_handler.go"))
      .toBe("internal/api/{old_handler.go => new_handler.go}");
  });

  it("handles a move across directories with a shared file name", () => {
    expect(formatRenamePath("pkg/a/util.go", "pkg/b/util.go"))
      .toBe("pkg/{a => b}/util.go");
  });
});

describe("fileKey", () => {
  it("disambiguates the same path with different statuses", () => {
    expect(fileKey({ path: "x.go", status: "staged" }))
      .not.toBe(fileKey({ path: "x.go", status: "unstaged" }));
    expect(fileKey({ path: "x.go" })).toBe(":x.go");
  });
});
