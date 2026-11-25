// @vitest-environment jsdom
import { StateEffect } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { describe, expect, it } from "vitest";
import { parseUnifiedDiff } from "./DiffViewer";
import {
  computeGitLineChanges,
  emptyGitLineChanges,
  gitChangeGutter,
  gitChangeGutterExtension,
  gitGutterColors,
  gitLineChangesForFile,
  gitOverviewMarks,
  hasGitLineChanges,
  setGitLineChanges,
} from "./editorGitGutter";

const MODIFIED_DIFF = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main
-func a() {}
+func b() {}
 func keep() {}
`;

const PURE_DELETE_DIFF = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,2 @@
 package main
-func gone() {}
 func keep() {}
`;

describe("computeGitLineChanges", () => {
  it("classifies a del+add run as modified", () => {
    const changes = computeGitLineChanges(parseUnifiedDiff(MODIFIED_DIFF));
    expect(changes.changed.get(2)).toBe("modified");
    expect(changes.deletedAbove.size).toBe(0);
    expect(changes.deletedAtEnd).toBe(false);
  });

  it("classifies an add-only run as added", () => {
    const diff = `diff --git a/m.go b/m.go
--- a/m.go
+++ b/m.go
@@ -1,2 +1,3 @@
 package main
+func fresh() {}
 func keep() {}
`;
    const changes = computeGitLineChanges(parseUnifiedDiff(diff));
    expect(changes.changed.get(2)).toBe("added");
  });

  it("anchors a pure deletion to the line below it", () => {
    const changes = computeGitLineChanges(parseUnifiedDiff(PURE_DELETE_DIFF));
    expect(changes.changed.size).toBe(0);
    expect([...changes.deletedAbove]).toEqual([2]);
  });

  it("flags a deletion at end-of-file", () => {
    const diff = `diff --git a/m.go b/m.go
--- a/m.go
+++ b/m.go
@@ -1,2 +1,1 @@
 package main
-func tail() {}
`;
    const changes = computeGitLineChanges(parseUnifiedDiff(diff));
    expect(changes.deletedAtEnd).toBe(true);
  });

  it("merges hunks from a partially-staged file (two ParsedFile entries)", () => {
    const both = parseUnifiedDiff(MODIFIED_DIFF, "staged").concat(parseUnifiedDiff(PURE_DELETE_DIFF, "unstaged"));
    const changes = computeGitLineChanges(both);
    expect(changes.changed.get(2)).toBe("modified");
    expect(changes.deletedAbove.has(2)).toBe(true);
  });
});

describe("computeGitLineChanges with hand-built hunks", () => {
  it("ignores an added line without a new-file number", () => {
    const changes = computeGitLineChanges([{ path: "m.go", hunks: [{ header: "@@", lines: [{ type: "add", content: "x", oldNum: null, newNum: null }] }] }]);
    expect(hasGitLineChanges(changes)).toBe(false);
  });

  it("anchors a deletion past lines without a new-file number", () => {
    const changes = computeGitLineChanges([
      {
        path: "m.go",
        hunks: [
          {
            header: "@@",
            lines: [
              { type: "del", content: "gone", oldNum: 1, newNum: null },
              { type: "ctx", content: "", oldNum: 2, newNum: null },
              { type: "ctx", content: "keep", oldNum: 3, newNum: 5 },
            ],
          },
        ],
      },
    ]);
    expect([...changes.deletedAbove]).toEqual([5]);
  });
});

describe("gitLineChangesForFile", () => {
  it("selects only the matching path from a combined diff", () => {
    const other = MODIFIED_DIFF.replace(/main\.go/g, "other.go");
    const changes = gitLineChangesForFile(MODIFIED_DIFF + other, "other.go");
    expect(changes.changed.get(2)).toBe("modified");
    expect(gitLineChangesForFile(MODIFIED_DIFF, "absent.go").changed.size).toBe(0);
  });

  it("returns empty changes for empty inputs", () => {
    expect(hasGitLineChanges(gitLineChangesForFile("", "main.go"))).toBe(false);
    expect(hasGitLineChanges(gitLineChangesForFile(MODIFIED_DIFF, ""))).toBe(false);
  });
});

describe("hasGitLineChanges", () => {
  it("is false for the empty value and true for each kind of change", () => {
    expect(hasGitLineChanges(emptyGitLineChanges())).toBe(false);

    const withChange = emptyGitLineChanges();
    withChange.changed.set(1, "added");
    expect(hasGitLineChanges(withChange)).toBe(true);

    const withDelete = emptyGitLineChanges();
    withDelete.deletedAbove.add(3);
    expect(hasGitLineChanges(withDelete)).toBe(true);

    const withTail = emptyGitLineChanges();
    withTail.deletedAtEnd = true;
    expect(hasGitLineChanges(withTail)).toBe(true);
  });
});

describe("gitOverviewMarks", () => {
  it("coalesces consecutive same-kind lines into one proportional block", () => {
    const changes = emptyGitLineChanges();
    changes.changed.set(11, "added");
    changes.changed.set(12, "added");
    changes.changed.set(13, "added");
    changes.changed.set(20, "modified");

    const marks = gitOverviewMarks(changes, 100);
    expect(marks).toHaveLength(2);
    expect(marks[0]).toMatchObject({ kind: "added", line: 11, topFrac: 0.1 });
    expect(marks[0]!.heightFrac).toBeCloseTo(0.03);
    expect(marks[1]).toMatchObject({ kind: "modified", line: 20 });
  });

  it("does not coalesce across a kind change", () => {
    const changes = emptyGitLineChanges();
    changes.changed.set(5, "added");
    changes.changed.set(6, "modified");
    expect(gitOverviewMarks(changes, 10)).toHaveLength(2);
  });

  it("emits zero-height deletion marks and handles deletedAtEnd", () => {
    const changes = emptyGitLineChanges();
    changes.deletedAbove.add(4);
    changes.deletedAtEnd = true;
    const marks = gitOverviewMarks(changes, 10);
    expect(marks).toHaveLength(2);
    expect(marks.every((m) => m.kind === "deleted" && m.heightFrac === 0)).toBe(true);
    expect(marks[1]!.line).toBe(10);
  });

  it("returns no marks for an empty document", () => {
    const changes = emptyGitLineChanges();
    changes.changed.set(1, "added");
    expect(gitOverviewMarks(changes, 0)).toEqual([]);
  });
});

describe("gitGutterColors", () => {
  it("returns distinct colors per kind for both palettes", () => {
    for (const isDark of [true, false]) {
      const c = gitGutterColors(isDark);
      expect(new Set([c.added, c.modified, c.deleted]).size).toBe(3);
    }
    expect(gitGutterColors(true).added).not.toBe(gitGutterColors(false).added);
  });
});

describe("gitChangeGutter", () => {
  const doc = ["one", "two", "three", "four", "five"].join("\n");
  const marks = (view: EditorView) => Array.from(view.dom.querySelectorAll(".cm-gitChangeGutter .cm-gutterElement"), (el) => el.querySelector(".cm-gitChange")?.className ?? "");

  it("marks added, modified and deleted lines, and the deletion at the end", () => {
    const view = new EditorView({ doc, extensions: gitChangeGutterExtension, parent: document.body });
    expect(marks(view).filter(Boolean)).toEqual([]);
    const changes = emptyGitLineChanges();
    changes.changed.set(1, "added");
    changes.changed.set(2, "modified");
    changes.deletedAbove.add(4);
    changes.deletedAtEnd = true;
    view.dispatch({ effects: setGitLineChanges.of(changes) });
    expect(marks(view).filter(Boolean)).toEqual(["cm-gitChange cm-gitChange-added", "cm-gitChange cm-gitChange-modified", "cm-gitChange cm-gitChange-deleted", "cm-gitChange cm-gitChange-deleted"]);
    view.destroy();
  });

  it("keeps the markers through edits that bring no new changes", () => {
    const view = new EditorView({ doc, extensions: gitChangeGutterExtension, parent: document.body });
    const changes = emptyGitLineChanges();
    changes.changed.set(3, "added");
    view.dispatch({ effects: setGitLineChanges.of(changes) });
    view.dispatch({ changes: { from: 0, insert: "x" }, effects: StateEffect.define<null>().of(null) });
    expect(marks(view).filter(Boolean)).toEqual(["cm-gitChange cm-gitChange-added"]);
    view.destroy();
  });

  it("redraws a line whose kind of change changes", () => {
    const view = new EditorView({ doc, extensions: gitChangeGutterExtension, parent: document.body });
    const added = emptyGitLineChanges();
    added.changed.set(3, "added");
    view.dispatch({ effects: setGitLineChanges.of(added) });
    const modified = emptyGitLineChanges();
    modified.changed.set(3, "modified");
    view.dispatch({ effects: setGitLineChanges.of(modified) });
    expect(marks(view).filter(Boolean)).toEqual(["cm-gitChange cm-gitChange-modified"]);
    view.destroy();
  });

  it("marks nothing without the state field", () => {
    const view = new EditorView({ doc, extensions: gitChangeGutter, parent: document.body });
    expect(marks(view).filter(Boolean)).toEqual([]);
    view.destroy();
  });
});
