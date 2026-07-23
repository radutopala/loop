import { describe, expect, it } from "vitest";
import type { RootEntry } from "../api/files";
import { matchAbsPathToKey } from "./editorPaths";

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
