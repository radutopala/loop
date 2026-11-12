import { describe, expect, it } from "vitest";
import { fileWatchMessage, parseFileWatchChange } from "./fileWatch";

describe("fileWatchMessage", () => {
  it("sends open tabs as files and expanded dirs as dirs", () => {
    expect(JSON.parse(fileWatchMessage(["0:src/a.ts", "1:b.md"], new Set(["0:", "1:docs"])))).toEqual({
      type: "watch",
      files: [
        { root: 0, path: "src/a.ts" },
        { root: 1, path: "b.md" },
      ],
      dirs: [
        { root: 0, path: "" },
        { root: 1, path: "docs" },
      ],
    });
  });
});

describe("parseFileWatchChange", () => {
  it.each([
    ["a file change", '{"type":"file","root":1,"path":"a.ts"}', { type: "file", pathKey: "1:a.ts" }],
    ["a root dir change (path omitted)", '{"type":"dir","root":0}', { type: "dir", pathKey: "0:" }],
    ["an error", '{"type":"error","root":0,"message":"x"}', null],
    ["broken JSON", "{", null],
    ["a binary frame", new ArrayBuffer(1), null],
  ])("%s", (_name, data, want) => {
    expect(parseFileWatchChange(data)).toEqual(want);
  });
});
