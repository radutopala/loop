import { describe, expect, it } from "vitest";
import { previewDictation, spliceDictation } from "./dictationText";

describe("spliceDictation", () => {
  it.each([
    ["an empty composer", { text: "", start: 0, end: 0 }, "hello there", "hello there", 0],
    ["the end of a sentence", { text: "Note:", start: 5, end: 5 }, "hello", "Note: hello", 6],
    ["after a space", { text: "Note: ", start: 6, end: 6 }, "hello", "Note: hello", 6],
    ["the middle of text", { text: "ab", start: 1, end: 1 }, "x", "a x b", 2],
    ["the start of text", { text: "world", start: 0, end: 0 }, "hello", "hello world", 0],
    ["a selection, replaced", { text: "say WHAT now", start: 4, end: 8 }, "this", "say this now", 4],
    ["a caret past the end", { text: "ab", start: 9, end: 9 }, "c", "ab c", 3],
    ["next to a newline", { text: "a\n", start: 2, end: 2 }, "b", "a\nb", 2],
  ])("inserts at %s", (_name, base, words, want, from) => {
    const out = spliceDictation(base, words);
    expect(out.text).toBe(want);
    expect(out.from).toBe(from);
    expect(out.text.slice(out.from, out.to)).toBe(words);
  });

  it("leaves the text alone for empty words, caret where it was", () => {
    expect(spliceDictation({ text: "keep", start: 2, end: 4 }, "  ")).toEqual({ text: "keep", from: 2, to: 2 });
  });

  it("trims the words", () => {
    expect(spliceDictation({ text: "", start: 0, end: 0 }, " hi ").text).toBe("hi");
  });
});

describe("previewDictation", () => {
  const base = { text: "Q: ?", start: 3, end: 3 };

  it("shows committed then tentative, marking where the tentative tail starts", () => {
    const p = previewDictation(base, "how are", "you");
    expect(p.text).toBe("Q: how are you ?");
    expect(p.text.slice(p.from, p.tentativeFrom)).toBe("how are ");
    expect(p.text.slice(p.tentativeFrom, p.to)).toBe("you");
  });

  it("marks no tail when everything is committed", () => {
    const p = previewDictation(base, "done", "");
    expect(p.text).toBe("Q: done ?");
    expect(p.tentativeFrom).toBe(p.to);
  });

  it("is all tail before anything is committed", () => {
    const p = previewDictation(base, "", "maybe");
    expect(p.tentativeFrom).toBe(p.from);
    expect(p.text.slice(p.from, p.to)).toBe("maybe");
  });

  it("is the original text before any words", () => {
    expect(previewDictation(base, "", "").text).toBe("Q: ?");
  });
});
