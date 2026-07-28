import { describe, expect, it } from "vitest";
import type { ReviewComment } from "../../api/review";
import { parseUnifiedDiff } from "./DiffViewer";
import { type FileSummary, orderedComments } from "./ReviewDiffView";

const DIFF = `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package main
-func old() {}
+func one() {}
+func two() {}
@@ -20,2 +21,3 @@
 var x = 1
+var y = 2
diff --git a/b.go b/b.go
index 333..444 100644
--- a/b.go
+++ b/b.go
@@ -1,2 +1,3 @@
 package b
+var z = 3
`;

function comment(id: string, path: string, line: number, extra: Partial<ReviewComment> = {}): ReviewComment {
  return { id, path, line, side: "RIGHT", body: id, pushed: false, ...extra };
}

// Mirror what ReviewDiffView builds from the parsed diff, so ordering is
// exercised against real hunk geometry rather than hand-written line lists.
function summarize(rawDiff: string, comments: ReviewComment[]) {
  const parsedFiles = parseUnifiedDiff(rawDiff);
  const known = new Set(parsedFiles.map((p) => p.path));
  const byFile = new Map<string, ReviewComment[]>();
  const orphans: ReviewComment[] = [];
  for (const c of comments) {
    if (known.has(c.path)) byFile.set(c.path, [...(byFile.get(c.path) ?? []), c]);
    else orphans.push(c);
  }
  const summaries: FileSummary[] = parsedFiles.map((p) => ({
    path: p.path,
    additions: 0,
    deletions: 0,
    parsed: p,
    agentCount: (byFile.get(p.path) ?? []).length,
    ghCount: 0,
  }));
  return { summaries, byFile, orphans };
}

function ids(rawDiff: string, comments: ReviewComment[]): string[] {
  const { summaries, byFile, orphans } = summarize(rawDiff, comments);
  return orderedComments(summaries, byFile, orphans).map((a) => a.id);
}

describe("orderedComments", () => {
  it("orders by file, then by the diff line each comment anchors to", () => {
    // Supplied deliberately out of order: b.go before a.go, and within a.go
    // the second hunk's line before the first hunk's.
    const out = ids(DIFF, [comment("b1", "b.go", 2), comment("a-late", "a.go", 22), comment("a-early", "a.go", 2)]);
    expect(out).toEqual(["a-early", "a-late", "b1"]);
  });

  it("keeps several comments on one line in array order", () => {
    const out = ids(DIFF, [comment("first", "a.go", 2), comment("second", "a.go", 2), comment("third", "a.go", 2)]);
    expect(out).toEqual(["first", "second", "third"]);
  });

  it("puts out-of-diff comments last, after every file", () => {
    const out = ids(DIFF, [comment("orphan", "gone.go", 5), comment("a1", "a.go", 2), comment("b1", "b.go", 2)]);
    expect(out).toEqual(["a1", "b1", "orphan"]);
  });

  it("drops a comment whose line falls outside every hunk", () => {
    // Line 900 is in no hunk, so FileSection renders no card for it —
    // counting it would leave the navigator with a dead target.
    expect(ids(DIFF, [comment("a1", "a.go", 2), comment("nowhere", "a.go", 900)])).toEqual(["a1"]);
  });

  it("anchors a LEFT-side comment on the deleted line's old number", () => {
    // `func old() {}` is old line 2 and has no new number.
    expect(ids(DIFF, [comment("del", "a.go", 2, { side: "LEFT" })])).toEqual(["del"]);
    // The same line number on the RIGHT is a different, also-valid anchor.
    expect(ids(DIFF, [comment("add", "a.go", 2, { side: "RIGHT" })])).toEqual(["add"]);
  });

  it("carries the file index so navigation can expand the right section", () => {
    const { summaries, byFile, orphans } = summarize(DIFF, [comment("a1", "a.go", 2), comment("b1", "b.go", 2), comment("orphan", "gone.go", 1)]);
    expect(orderedComments(summaries, byFile, orphans)).toEqual([
      { id: "a1", path: "a.go", fileIdx: 0 },
      { id: "b1", path: "b.go", fileIdx: 1 },
      { id: "orphan", path: "gone.go", fileIdx: -1 },
    ]);
  });

  it("returns nothing when there are no comments", () => {
    expect(ids(DIFF, [])).toEqual([]);
  });
});
