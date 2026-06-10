import { describe, expect, it } from "vitest";
import { findCandidatePaths } from "./fileLinks";

const raws = (text: string) => findCandidatePaths(text).map((c) => c.raw);

describe("findCandidatePaths", () => {
  it("detects a relative path with an extension", () => {
    const found = findCandidatePaths("see src/components/Foo.tsx for details");
    expect(found).toHaveLength(1);
    expect(found[0]).toMatchObject({ raw: "src/components/Foo.tsx", line: null, start: 4 });
    expect(found[0]!.length).toBe("src/components/Foo.tsx".length);
  });

  it("detects an absolute path", () => {
    expect(raws("open /Users/me/dev/loop/main.go now")).toEqual(["/Users/me/dev/loop/main.go"]);
  });

  it("parses a trailing :line suffix and reports the full matched length", () => {
    const found = findCandidatePaths("internal/api/server.go:142");
    expect(found).toHaveLength(1);
    expect(found[0]).toMatchObject({ raw: "internal/api/server.go", line: 142 });
    expect(found[0]!.length).toBe("internal/api/server.go:142".length);
  });

  it("requires at least two segments — does not match a bare domain", () => {
    expect(findCandidatePaths("visit example.com or foo.org")).toEqual([]);
  });

  it("skips paths embedded in http(s):// and file:// URLs", () => {
    expect(findCandidatePaths("https://example.com/path/file.ext")).toEqual([]);
    expect(findCandidatePaths("file:///etc/hosts.txt")).toEqual([]);
  });

  it("excludes trailing punctuation from the match", () => {
    expect(raws("edit src/foo.ts.")).toEqual(["src/foo.ts"]);
    expect(raws("(see src/foo.ts)")).toEqual(["src/foo.ts"]);
    expect(raws("files: a/b.ts, c/d.ts; done")).toEqual(["a/b.ts", "c/d.ts"]);
  });

  it("finds multiple paths and reports correct offsets", () => {
    const text = "a/one.ts then b/two.go";
    const found = findCandidatePaths(text);
    expect(found.map((c) => c.raw)).toEqual(["a/one.ts", "b/two.go"]);
    expect(text.slice(found[0]!.start, found[0]!.start + found[0]!.length)).toBe("a/one.ts");
    expect(text.slice(found[1]!.start, found[1]!.start + found[1]!.length)).toBe("b/two.go");
  });

  it("returns nothing for empty or path-free text", () => {
    expect(findCandidatePaths("")).toEqual([]);
    expect(findCandidatePaths("just some prose with no paths at all")).toEqual([]);
  });

  it("does not match a path without an extension", () => {
    expect(findCandidatePaths("src/components/index")).toEqual([]);
  });

  it("does not treat a decimal number as a path", () => {
    expect(findCandidatePaths("the value was 3.14 today")).toEqual([]);
  });

  it("ignores a non-numeric pseudo line suffix", () => {
    // `:abc` is not a line number, so it is not consumed and the boundary
    // lookahead prevents a partial match.
    const found = findCandidatePaths("src/foo.ts:abc");
    // Either no match (boundary) — assert it does not report a bogus line.
    for (const c of found) expect(c.line).toBeNull();
  });
});
