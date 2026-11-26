import { describe, expect, it } from "vitest";
import { MAX_TAG_LENGTH, normalizeTag, tagError } from "./tags";

describe("normalizeTag", () => {
  it("trims, lowercases and drops a leading hash", () => {
    expect(normalizeTag("  #Front-End ")).toBe("front-end");
  });
});

describe("tagError", () => {
  it.each([
    ["", null],
    ["bug", null],
    ["team/web_v1.2", null],
    ["Ünïcode", null],
    ["two words", "Only letters, digits and - _ . /, starting with a letter or digit"],
    ["tag:x", "Only letters, digits and - _ . /, starting with a letter or digit"],
    ["-x", "Only letters, digits and - _ . /, starting with a letter or digit"],
    ["a".repeat(MAX_TAG_LENGTH + 1), `Longer than ${MAX_TAG_LENGTH} characters`],
    // Counted in bytes, as the backend does.
    ["ü".repeat(17), `Longer than ${MAX_TAG_LENGTH} characters`],
  ])("%j: %s", (value, want) => {
    expect(tagError(value)).toBe(want);
  });
});
