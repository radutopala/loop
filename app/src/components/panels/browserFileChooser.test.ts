import { describe, expect, it } from "vitest";
import { FILE_CHOOSER_MAX_BYTES, fileChooserProblem, type PageFileChooser, readChosenFiles, toBase64, withFileChooser, withoutFileChooser } from "./browserFileChooser";

const on = (target_id: string, multiple = false): PageFileChooser => ({ target_id, multiple });

describe("withFileChooser / withoutFileChooser", () => {
  it("keeps one chooser per tab, the latest winning", () => {
    let c = withFileChooser({}, on("t1"));
    c = withFileChooser(c, on("t2"));
    c = withFileChooser(c, on("t1", true));
    expect(Object.keys(c)).toEqual(["t1", "t2"]);
    expect(c.t1?.multiple).toBe(true);
  });

  it("drops a tab's chooser and leaves the others", () => {
    const c = withoutFileChooser(withFileChooser(withFileChooser({}, on("t1")), on("t2")), "t1");
    expect(Object.keys(c)).toEqual(["t2"]);
  });

  it("returns the same map when the tab had no chooser, so state does not change", () => {
    const c = withFileChooser({}, on("t1"));
    expect(withoutFileChooser(c, "t9")).toBe(c);
  });
});

describe("fileChooserProblem", () => {
  it.each<[string, number[], string | null]>([
    ["nothing picked", [], null],
    ["right at the limit", [FILE_CHOOSER_MAX_BYTES - 1, 1], null],
    ["one byte over, across files", [FILE_CHOOSER_MAX_BYTES, 1], "The files are over the 20 MB limit."],
  ])("%s", (_, sizes, want) => {
    expect(fileChooserProblem(sizes.map((size) => ({ size })))).toBe(want);
  });
});

describe("toBase64", () => {
  it.each<[string, number[], string]>([
    ["empty", [], ""],
    ["text", [104, 105], "aGk="],
    ["every byte value", [0, 127, 128, 255], "AH+A/w=="],
  ])("%s", (_, bytes, want) => {
    expect(toBase64(new Uint8Array(bytes))).toBe(want);
  });

  it("encodes a file bigger than one chunk without overflowing the stack", () => {
    const bytes = new Uint8Array(0x8000 * 3 + 5).map((_, i) => i % 256);
    expect(toBase64(bytes)).toBe(Buffer.from(bytes).toString("base64"));
  });
});

describe("readChosenFiles", () => {
  it("reads each file's name, type and bytes", async () => {
    const files = [new File(["hi"], "a.txt", { type: "text/plain" }), new File([new Uint8Array([255])], "b.bin")];
    expect(await readChosenFiles(files)).toEqual([
      { name: "a.txt", type: "text/plain", data: "aGk=" },
      { name: "b.bin", type: "", data: "/w==" },
    ]);
  });
});
