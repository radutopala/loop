import { describe, expect, it } from "vitest";
import type { RootEntry } from "../api/files";
import { editToolFilePath, matchAbsPathToKey, type TabPathInfo, tabDescriptions } from "./editorPaths";

function root(index: number, path: string): RootEntry {
  return { index, path, name: path.split("/").pop() || path };
}

describe("matchAbsPathToKey", () => {
  it("maps a path under a single root to {index}:{relpath}", () => {
    const roots = [root(0, "/home/user/project")];
    expect(matchAbsPathToKey("/home/user/project/src/foo.ts", roots)).toBe("0:src/foo.ts");
  });

  it("maps a path equal to a root to an empty relative path", () => {
    const roots = [root(0, "/home/user/project")];
    expect(matchAbsPathToKey("/home/user/project", roots)).toBe("0:");
  });

  it("returns null when no root contains the path", () => {
    const roots = [root(0, "/home/user/project")];
    expect(matchAbsPathToKey("/elsewhere/file.ts", roots)).toBeNull();
    expect(matchAbsPathToKey("/file.ts", roots)).toBeNull();
  });

  it("picks the LONGEST matching root for nested roots", () => {
    // The inner root must win so the relative path is anchored to the most
    // specific workspace, not the outer one.
    const roots = [root(0, "/repo"), root(1, "/repo/sub")];
    expect(matchAbsPathToKey("/repo/sub/a.ts", roots)).toBe("1:a.ts");
    // A path only under the outer root resolves to it.
    expect(matchAbsPathToKey("/repo/top.ts", roots)).toBe("0:top.ts");
  });

  it("picks the longest root regardless of declaration order", () => {
    const roots = [root(0, "/repo/sub"), root(1, "/repo")];
    expect(matchAbsPathToKey("/repo/sub/a.ts", roots)).toBe("0:a.ts");
  });

  it("does not treat a sibling with a shared prefix as a match", () => {
    // "/repo/foobar" must NOT match root "/repo/foo" (prefix-without-separator).
    const roots = [root(0, "/repo/foo")];
    expect(matchAbsPathToKey("/repo/foobar/x.ts", roots)).toBeNull();
  });

  it("normalizes a trailing slash on the root path", () => {
    const roots = [root(0, "/home/user/project/")];
    expect(matchAbsPathToKey("/home/user/project/src/foo.ts", roots)).toBe("0:src/foo.ts");
    expect(matchAbsPathToKey("/home/user/project", roots)).toBe("0:");
  });

  it("returns null for an empty roots list", () => {
    expect(matchAbsPathToKey("/anything.ts", [])).toBeNull();
  });

  it("preserves nested relative paths under the matched root", () => {
    const roots = [root(2, "/x/y")];
    expect(matchAbsPathToKey("/x/y/a/b/c.go", roots)).toBe("2:a/b/c.go");
  });
});

describe("editToolFilePath", () => {
  it.each<[string, string, string | null]>([
    ["Edit", "/r/a.ts", "/r/a.ts"],
    ["Write", "/r/b.ts", "/r/b.ts"],
    ["Read", "/r/a.ts", null],
    ["Bash", "ls /r", null],
    ["Edit", "", null],
    ["Edit", JSON.stringify({ file_path: "/r/a.ts" }), null],
  ])("%s %s → %s", (tool, input, want) => {
    expect(editToolFilePath(tool, input)).toBe(want);
  });
});

describe("tabDescriptions", () => {
  const tab = (key: string, rootName: string, relativePath: string): TabPathInfo => ({ key, rootName, relativePath });

  it.each<[string, TabPathInfo[], boolean, Record<string, string>]>([
    ["single root, file at the root", [tab("a", "loop", "go.mod")], false, { a: "" }],
    ["single root, nested file shows its parent folder", [tab("a", "loop", "container/Dockerfile")], false, { a: "container" }],
    ["multiple roots lead with the root name", [tab("a", ".loop", "container/Dockerfile"), tab("b", "loop", "container/Dockerfile")], true, { a: ".loop › container", b: "loop › container" }],
    ["multiple roots, file at a root shows the root only", [tab("a", ".loop", "config.json")], true, { a: ".loop" }],
    [
      "same name in the same root adds parents until distinct",
      [tab("a", "loop", "pkg/app/src/index.ts"), tab("b", "loop", "pkg/web/src/index.ts"), tab("c", "loop", "cmd/main.go")],
      false,
      { a: "…/app/src", b: "…/web/src", c: "cmd" },
    ],
    ["the whole folder path is shown without an ellipsis", [tab("a", "loop", "a/b/x.ts"), tab("b", "loop", "c/b/x.ts")], false, { a: "a/b", b: "c/b" }],
    ["a root-level duplicate keeps an empty folder", [tab("a", "loop", "x.ts"), tab("b", "loop", "src/x.ts")], false, { a: "", b: "src" }],
    ["same name in different roots isn't a duplicate", [tab("a", "loop", "deep/src/x.ts"), tab("b", "other", "more/src/x.ts")], true, { a: "loop › …/src", b: "other › …/src" }],
  ])("%s", (_name, tabs, showRoot, want) => {
    expect(Object.fromEntries(tabDescriptions(tabs, showRoot))).toEqual(want);
  });
});
