import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checkFilesExist } from "../api/files";
import { commandDirs, findCandidatePaths, getValidationStatus, pathInDir, requestValidation, resetFileLinkCache, retain, revalidateMissing } from "./fileLinks";

vi.mock("../api/files", () => ({ checkFilesExist: vi.fn() }));

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

describe("commandDirs", () => {
  it.each([
    ["cd /w/tree && cat a/b.txt", ["/w/tree"]],
    ["cd /w && cd sub/dir; ls", ["/w", "/w/sub/dir"]],
    ["cd /w/a/ && cd ../b", ["/w/a", "/w/b"]],
    ['cd "/w/with space" && ls', ["/w/with space"]],
    ["echo hi\ncd '/w/q'", ["/w/q"]],
    ["cd sub && ls", []],
    ["cd ~/x; cd $HOME/y", []],
    ["abcd /nope; echo cd /nope", []],
  ])("%s", (cmd, want) => {
    expect(commandDirs(cmd).map((d) => d.dir)).toEqual(want);
  });
});

describe("pathInDir", () => {
  const cmd = "cat x/a.md; cd /w/tree && cat memory/b.yaml ./c/d.json /abs/e.txt";
  const dirs = commandDirs(cmd);
  it.each([
    ["x/a.md", "x/a.md"],
    ["memory/b.yaml", "/w/tree/memory/b.yaml"],
    ["./c/d.json", "/w/tree/c/d.json"],
    ["/abs/e.txt", "/abs/e.txt"],
  ])("%s", (raw, want) => {
    expect(pathInDir(raw, cmd.indexOf(raw), dirs)).toBe(want);
  });
  it("climbs no higher than the root", () => {
    expect(pathInDir("../../../a/b.txt", 20, [{ end: 0, dir: "/w" }])).toBe("/a/b.txt");
  });
});

describe("revalidateMissing", () => {
  const exists = vi.mocked(checkFilesExist);
  const settle = async () => {
    await vi.advanceTimersByTimeAsync(100);
  };
  beforeEach(() => {
    vi.useFakeTimers();
    resetFileLinkCache();
    exists.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("checks again a shown path that wasn't found, and only that one", async () => {
    exists.mockResolvedValueOnce([
      { path: "a/new.txt", exists: false },
      { path: "a/hidden.txt", exists: false },
      { path: "a/old.txt", exists: true, root_index: 0, rel_path: "a/old.txt" },
    ]);
    const release = retain("ch", "a/new.txt");
    retain("ch", "a/old.txt");
    retain("ch", "a/hidden.txt")();
    for (const p of ["a/new.txt", "a/hidden.txt", "a/old.txt"]) requestValidation("ch", p);
    await settle();
    expect(getValidationStatus("ch", "a/new.txt").kind).toBe("invalid");

    exists.mockResolvedValueOnce([{ path: "a/new.txt", exists: true, root_index: 0, rel_path: "a/new.txt" }]);
    revalidateMissing("ch");
    await settle();
    expect(exists).toHaveBeenLastCalledWith("ch", ["a/new.txt"]);
    expect(getValidationStatus("ch", "a/new.txt")).toEqual({ kind: "valid", target: { rootIndex: 0, relPath: "a/new.txt" } });
    expect(getValidationStatus("ch", "a/hidden.txt").kind).toBe("invalid");

    // Released, it isn't checked again.
    release();
    exists.mockClear();
    revalidateMissing("ch");
    revalidateMissing("other");
    await settle();
    expect(exists).not.toHaveBeenCalled();
  });

  it("keeps a path shown while any link still shows it", async () => {
    exists.mockResolvedValue([{ path: "a/x.txt", exists: false }]);
    const first = retain("ch", "a/x.txt");
    retain("ch", "a/x.txt");
    requestValidation("ch", "a/x.txt");
    await settle();
    first();
    revalidateMissing("ch");
    await settle();
    expect(exists).toHaveBeenCalledTimes(2);
  });
});
