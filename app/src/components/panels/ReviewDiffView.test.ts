import { describe, expect, it } from "vitest";
import type { ReviewComment } from "../../api/review";
import type { ColorPalette } from "../../theme";
import { parseUnifiedDiff } from "./DiffViewer";
import {
  type CommentAnchor,
  commentEmphasis,
  commentImpact,
  commentKind,
  type FileSummary,
  impactColor,
  navigableAnchors,
  orderedComments,
  splitComments,
  threadOrder,
  unlandedTarget,
  verdictColor,
  verdictLabel,
} from "./ReviewDiffView";

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
  const { byFile, orphans } = splitComments(parsedFiles, comments);
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

  it("puts a comment whose line falls outside every hunk with the out-of-diff ones", () => {
    // The line has no row to render under, so it's an orphan the navigator
    // still counts, like the menus that act on every comment.
    expect(ids(DIFF, [comment("nowhere", "a.go", 900), comment("a1", "a.go", 2), comment("left", "b.go", 2, { side: "LEFT" })])).toEqual(["a1", "nowhere", "left"]);
    const { byFile, orphans } = splitComments(parseUnifiedDiff(DIFF), [comment("nowhere", "a.go", 900), comment("a1", "a.go", 2)]);
    expect([...byFile.keys()]).toEqual(["a.go"]);
    expect(orphans.map((c) => c.id)).toEqual(["nowhere"]);
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
      { id: "a1", path: "a.go", fileIdx: 0, fresh: false, kind: "unjudged" },
      { id: "b1", path: "b.go", fileIdx: 1, fresh: false, kind: "unjudged" },
      { id: "orphan", path: "gone.go", fileIdx: -1, fresh: false, kind: "unjudged" },
    ]);
  });

  it("returns nothing when there are no comments", () => {
    expect(ids(DIFF, [])).toEqual([]);
  });
});

describe("navigableAnchors", () => {
  it("flags only the agent findings of the latest review's runs as fresh", () => {
    const { summaries, byFile, orphans } = summarize(DIFF, [
      comment("latest", "a.go", 2, { run_id: 8 }),
      comment("latest-pushed", "a.go", 3, { run_id: 9, pushed: true }),
      comment("earlier", "a.go", 3, { run_id: 7 }),
      comment("no-run", "a.go", 3),
      comment("gh", "b.go", 2, { source: "github", pushed: true }),
      comment("orphan", "gone.go", 1, { run_id: 9 }),
    ]);
    expect(orderedComments(summaries, byFile, orphans, [8, 9]).map((a) => [a.id, a.fresh])).toEqual([
      ["latest", true],
      ["latest-pushed", true],
      ["earlier", false],
      ["no-run", false],
      ["gh", false],
      ["orphan", true],
    ]);
  });

  it("carries each comment's kind", () => {
    const { summaries, byFile, orphans } = summarize(DIFF, [
      comment("blocking", "a.go", 2, { verdict: "real", impact: "blocking" }),
      comment("fp", "a.go", 3, { verdict: "false_positive" }),
      comment("reply", "b.go", 2, { source: "github", github_id: 2, reply_to: 1 }),
      comment("orphan", "gone.go", 1, { verdict: "already_fixed" }),
    ]);
    expect(orderedComments(summaries, byFile, orphans).map((a) => [a.id, a.kind])).toEqual([
      ["blocking", "blocking"],
      ["fp", "false_positive"],
      ["reply", undefined],
      ["orphan", "already_fixed"],
    ]);
  });

  const anchors: CommentAnchor[] = [
    { id: "latest", path: "a.go", fileIdx: 0, fresh: true, kind: "blocking" },
    { id: "latest-nit", path: "a.go", fileIdx: 0, fresh: true, kind: "nice_to_have" },
    { id: "earlier", path: "a.go", fileIdx: 0, fresh: false, kind: "blocking" },
    { id: "gh", path: "b.go", fileIdx: 1, fresh: false, kind: "unjudged" },
    { id: "gh-reply", path: "b.go", fileIdx: 1, fresh: false },
  ];

  it.each([
    ["all", "any", ["latest", "latest-nit", "earlier", "gh", "gh-reply"]],
    ["new", "any", ["latest", "latest-nit"]],
    ["all", "blocking", ["latest", "earlier"]],
    ["new", "blocking", ["latest"]],
    ["all", "unjudged", ["gh"]],
    ["new", "false_positive", []],
  ] as const)("scope %s, kind %s", (scope, kind, want) => {
    expect(navigableAnchors(anchors, scope, kind).map((a) => a.id)).toEqual(want);
  });

  it("walks every kind by default", () => {
    expect(navigableAnchors(anchors, "new").map((a) => a.id)).toEqual(["latest", "latest-nit"]);
  });
});

describe("unlandedTarget", () => {
  const view = { top: 100, bottom: 500 };
  const box = (top: number) => ({ top, bottom: top + 40 });

  it.each([
    // The bug: at the top of the diff the first comment is on screen, and
    // next must land on it rather than skip to the second.
    ["next lands on the first comment on screen", 1, [box(120), box(300)], 0],
    ["next skips comments scrolled above the view", 1, [box(0), box(300)], 1],
    ["next ignores collapsed comments", 1, [null, box(300)], 1],
    ["next falls back when all are above", 1, [box(0), box(20)], 9],
    ["prev lands on the last comment on screen", -1, [box(120), box(300), box(700)], 1],
    ["prev skips comments below the view", -1, [box(120), box(600)], 0],
    ["prev falls back when all are below", -1, [box(600), null], 9],
  ] as const)("%s", (_name, dir, rects, want) => {
    expect(unlandedTarget(dir, [...rects], view, 9)).toBe(want);
  });
});

describe("verdict badge", () => {
  const colors = { warning: "#warn", textDim: "#dim" } as ColorPalette;
  it.each([
    ["real", "verified", "#dim"],
    ["false_positive", "false positive", "#warn"],
    ["already_fixed", "already fixed", "#dim"],
  ] as const)("%s", (v, label, color) => {
    expect(verdictLabel(v)).toBe(label);
    expect(verdictColor(colors, v)).toBe(color);
  });
});

describe("impact badge", () => {
  const colors = { dangerText: "#danger", textDim: "#dim" } as ColorPalette;
  it.each([
    ["blocking", "#danger"],
    ["nice_to_have", "#dim"],
  ] as const)("%s", (impact, color) => {
    expect(impactColor(colors, impact)).toBe(color);
  });
});

describe("commentImpact", () => {
  it.each<[string, Partial<ReviewComment>, string | undefined]>([
    ["an agent finding's own", { source: "agent", verdict: "real", impact: "blocking" }, "blocking"],
    ["an agent finding without one", { source: "agent" }, undefined],
    ["an open thread's", { source: "github", github_id: 1, thread: { status: "open", impact: "nice_to_have" } }, "nice_to_have"],
    ["none for an addressed thread", { source: "github", github_id: 1, thread: { status: "addressed" } }, undefined],
    ["none for an unassessed thread", { source: "github", github_id: 1 }, undefined],
  ])("%s", (_name, extra, want) => {
    expect(commentImpact(comment("c", "a.go", 1, extra))).toBe(want);
  });
});

describe("commentKind", () => {
  it.each<[string, Partial<ReviewComment>, string | undefined]>([
    ["a blocking finding", { source: "agent", verdict: "real", impact: "blocking" }, "blocking"],
    ["a nice-to-have finding", { verdict: "real", impact: "nice_to_have" }, "nice_to_have"],
    ["a false positive", { verdict: "false_positive" }, "false_positive"],
    ["an already fixed finding", { verdict: "already_fixed" }, "already_fixed"],
    ["a finding not judged yet", { source: "agent" }, "unjudged"],
    ["an open blocking thread", { source: "github", github_id: 1, thread: { status: "open", impact: "blocking" } }, "blocking"],
    ["an addressed thread as fixed", { source: "github", github_id: 1, thread: { status: "addressed" } }, "already_fixed"],
    ["an unassessed thread", { source: "github", github_id: 1 }, "unjudged"],
    ["no kind for a reply", { source: "github", github_id: 2, reply_to: 1 }, undefined],
  ])("%s", (_name, extra, want) => {
    expect(commentKind(comment("c", "a.go", 1, extra))).toBe(want);
  });
});

describe("commentEmphasis", () => {
  const fresh = new Set(["new1", "new2"]);
  it.each<[string, string, string | null, string | undefined]>([
    ["the landed comment, new or not", "new1", "new1", "current"],
    ["the landed comment among old ones", "old", "old", "current"],
    ["another new comment", "new2", "new1", "fresh"],
    ["a new comment before landing", "new1", null, "fresh"],
    ["an old comment", "old", "new1", undefined],
  ])("%s", (_name, id, landedId, want) => {
    expect(commentEmphasis(id, landedId, fresh)).toBe(want);
  });
});

describe("threadOrder", () => {
  const gh = (id: string, githubId: number, replyTo?: number) => comment(id, "a.go", 2, { source: "github", github_id: githubId, reply_to: replyTo });

  it("moves replies right after their thread start, in order", () => {
    const cs = [gh("r1", 1), comment("agent", "a.go", 2), gh("r2", 2), gh("r1-a", 3, 1), gh("r2-a", 4, 2), gh("r1-b", 5, 1)];
    expect(threadOrder(cs).map((c) => c.id)).toEqual(["r1", "r1-a", "r1-b", "agent", "r2", "r2-a"]);
  });

  it("keeps a reply whose thread start isn't held in place", () => {
    const cs = [gh("lone", 9, 7), gh("r1", 1)];
    expect(threadOrder(cs)).toBe(cs);
  });

  it("orders the navigator by thread too", () => {
    expect(ids(DIFF, threadOrder([gh("r1", 1), gh("other", 2), gh("r1-a", 3, 1)]))).toEqual(["r1", "r1-a", "other"]);
  });
});
