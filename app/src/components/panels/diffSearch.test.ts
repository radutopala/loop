import { describe, expect, it } from "vitest";
import type { ParsedFile } from "./DiffViewer";
import { fuzzyScore, lineAddr, matchesInLine, parseQuery, searchContent, searchParsedFiles, searchPaths } from "./diffSearch";

describe("fuzzyScore", () => {
  it("returns null when the query is not a subsequence", () => {
    expect(fuzzyScore("xyz", "app/src/DiffViewer.tsx")).toBeNull();
  });

  it("matches an empty query with no positions", () => {
    expect(fuzzyScore("", "anything")).toEqual({ score: 0, positions: [] });
  });

  it("reports the positions it matched", () => {
    expect(fuzzyScore("dv", "DiffViewer")!.positions).toEqual([0, 4]);
  });

  it("ranks a path-segment start above a mid-word hit", () => {
    // Both contain "git" as a subsequence; only one starts a segment.
    const boundary = fuzzyScore("git", "app/git/panel.ts")!.score;
    const midWord = fuzzyScore("git", "app/legit/panel.ts")!.score;
    expect(boundary).toBeGreaterThan(midWord);
  });

  it("ranks camelCase humps above mid-word characters", () => {
    const humps = fuzzyScore("dv", "DiffViewer.tsx")!.score;
    const midWord = fuzzyScore("dv", "adverb.tsx")!.score;
    expect(humps).toBeGreaterThan(midWord);
  });

  it("ranks a consecutive run above the same characters split by a gap", () => {
    const run = fuzzyScore("diff", "diff.ts")!.score;
    const split = fuzzyScore("diff", "d_i_f_f.ts")!.score;
    expect(run).toBeGreaterThan(split);
  });

  it("tightens the start of the match rather than spanning the whole string", () => {
    // Forward pass ends on the trailing "t"; the backward pass then pulls the
    // "v" from index 0 up to index 7 so the run is as short as possible.
    expect(fuzzyScore("vt", "viewer/view.ts")!.positions).toEqual([7, 12]);
  });

  it("keeps V1's first-match behaviour when the query appears twice", () => {
    // Documented, not incidental: V1 stops at the first subsequence it finds,
    // which is what makes it two passes instead of a matrix.
    expect(fuzzyScore("view", "viewer/view.ts")!.positions).toEqual([0, 1, 2, 3]);
  });

  it("is case-insensitive for an all-lowercase query", () => {
    expect(fuzzyScore("readme", "README.md")).not.toBeNull();
  });

  it("is case-sensitive once the query has an uppercase character", () => {
    expect(fuzzyScore("Diff", "diffviewer.ts")).toBeNull();
    expect(fuzzyScore("Diff", "DiffViewer.ts")).not.toBeNull();
  });
});

describe("searchPaths", () => {
  const files = [{ path: "app/src/components/panels/GitPanel.tsx" }, { path: "app/src/components/panels/DiffViewer.tsx" }, { path: "internal/api/git_handler.go" }];

  it("returns nothing for a blank query", () => {
    expect(searchPaths(files, "   ")).toEqual([]);
  });

  it("drops non-matching files and keeps the file index", () => {
    const hits = searchPaths(files, "gitpanel");
    expect(hits).toHaveLength(1);
    expect(hits[0]!.fileIndex).toBe(0);
  });

  it("orders by score, best first", () => {
    // "git" starts a path segment in the second file but sits mid-word inside
    // "legit" in the first, so the ranking inverts the input order.
    const ranked = [{ path: "src/something/legit.ts" }, { path: "src/git/panel.ts" }];
    expect(searchPaths(ranked, "git").map((h) => h.fileIndex)).toEqual([1, 0]);
  });

  it("breaks ties on file order so results do not reshuffle", () => {
    const dupes = [{ path: "a/thing.ts" }, { path: "b/thing.ts" }];
    expect(searchPaths(dupes, "thing").map((h) => h.fileIndex)).toEqual([0, 1]);
  });
});

describe("matchesInLine", () => {
  it("finds every occurrence left to right", () => {
    expect(matchesInLine("foo bar foo", "foo")).toEqual([
      { start: 0, end: 3 },
      { start: 8, end: 11 },
    ]);
  });

  it("matches case-insensitively for an all-lowercase needle", () => {
    expect(matchesInLine("Foo", "foo")).toEqual([{ start: 0, end: 3 }]);
  });

  it("is case-sensitive once the needle has an uppercase character", () => {
    expect(matchesInLine("errtimeout", "errTimeout")).toEqual([]);
    expect(matchesInLine("errTimeout", "errTimeout")).toEqual([{ start: 0, end: 10 }]);
  });

  it("does not overlap matches", () => {
    expect(matchesInLine("aaaa", "aa")).toEqual([
      { start: 0, end: 2 },
      { start: 2, end: 4 },
    ]);
  });

  it("returns nothing for an empty needle", () => {
    expect(matchesInLine("foo", "")).toEqual([]);
  });
});

describe("searchContent", () => {
  const files = [
    { path: "a.go", status: undefined },
    { path: "b.go", status: undefined },
  ];
  const parsedFiles: ParsedFile[] = [
    {
      path: "a.go",
      hunks: [
        {
          header: "@@ -1 +1 @@",
          lines: [
            { type: "ctx", content: "package main", oldNum: 1, newNum: 1 },
            { type: "add", content: "  return errTimeout", oldNum: null, newNum: 2 },
          ],
        },
      ],
    },
    {
      path: "b.go",
      hunks: [{ header: "@@ -1 +1 @@", lines: [{ type: "del", content: "errTimeout = nil", oldNum: 1, newNum: null }] }],
    },
  ];

  it("returns nothing for a blank query", () => {
    expect(searchContent(files, parsedFiles, "  ")).toEqual([]);
  });

  it("walks files, hunks and lines in render order", () => {
    const hits = searchContent(files, parsedFiles, "errtimeout");
    expect(hits).toEqual([
      { fileIndex: 0, hunkIndex: 0, lineIndex: 1, start: 9, end: 19 },
      { fileIndex: 1, hunkIndex: 0, lineIndex: 0, start: 0, end: 10 },
    ]);
  });

  it("skips files with no parsed counterpart", () => {
    expect(searchContent([{ path: "gone.go", status: undefined }], parsedFiles, "errtimeout")).toEqual([]);
  });

  it("pairs on status so a partially staged path matches the right row", () => {
    const staged: ParsedFile = { path: "a.go", status: "staged", hunks: [{ header: "@@", lines: [{ type: "add", content: "hit", oldNum: null, newNum: 1 }] }] };
    const unstaged: ParsedFile = { path: "a.go", status: "unstaged", hunks: [{ header: "@@", lines: [{ type: "add", content: "miss", oldNum: null, newNum: 1 }] }] };
    const hits = searchContent([{ path: "a.go", status: "unstaged" }], [staged, unstaged], "miss");
    expect(hits).toHaveLength(1);
    expect(hits[0]!.fileIndex).toBe(0);
  });
});

describe("searchParsedFiles", () => {
  // The form the Review panel uses: it renders straight from ParsedFile[],
  // with no DiffFile list to pair against.
  const parsedFiles: ParsedFile[] = [
    { path: "a.go", hunks: [{ header: "@@", lines: [{ type: "add", content: "errTimeout", oldNum: null, newNum: 1 }] }] },
    { path: "b.go", hunks: [{ header: "@@", lines: [{ type: "ctx", content: "errTimeout errTimeout", oldNum: 1, newNum: 1 }] }] },
  ];

  it("returns nothing for a blank query", () => {
    expect(searchParsedFiles(parsedFiles, "  ")).toEqual([]);
  });

  it("indexes files by their position in the list it was given", () => {
    expect(searchParsedFiles(parsedFiles, "errtimeout").map((m) => m.fileIndex)).toEqual([0, 1, 1]);
  });

  it("reports every match in a line, left to right", () => {
    const hits = searchParsedFiles([parsedFiles[1]!], "errTimeout");
    expect(hits.map((m) => m.start)).toEqual([0, 11]);
  });

  it("skips a hole without shifting the indices after it", () => {
    // A caller whose list has a gap (no parsed counterpart for a row) must
    // still get indices that address its own rows.
    const hits = searchParsedFiles([undefined, parsedFiles[0]!], "errtimeout");
    expect(hits.map((m) => m.fileIndex)).toEqual([1]);
  });
});

describe("lineAddr", () => {
  it("builds a stable address", () => {
    expect(lineAddr(2, 0, 7)).toBe("2:0:7");
  });
});

describe("parseQuery", () => {
  it("treats bare text as a content search", () => {
    expect(parseQuery("errTimeout")).toEqual({ mode: "content", term: "errTimeout" });
  });

  it("treats a leading > as a path jump and trims the term", () => {
    expect(parseQuery("> diff viewer ")).toEqual({ mode: "path", term: "diff viewer" });
  });

  it("keeps content whitespace, which is searchable", () => {
    expect(parseQuery("  indent")).toEqual({ mode: "content", term: "  indent" });
  });
});
