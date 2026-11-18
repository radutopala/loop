import { describe, expect, it } from "vitest";

import { BARE_URL, trimUrl } from "./bareUrl";

const find = (text: string) => trimUrl(new RegExp(BARE_URL).exec(text)?.[0] ?? "");

describe("bare URLs", () => {
  it.each([
    ["a plain link", "see https://example.test/x now", "https://example.test/x"],
    ["the end of a sentence", "See https://example.test/x.", "https://example.test/x"],
    ["a comma after it", "https://example.test/x, then", "https://example.test/x"],
    ["a closing parenthesis it didn't open", "(see https://example.test/x)", "https://example.test/x"],
    ["its own parentheses", "https://example.test/wiki/Foo_(bar) here", "https://example.test/wiki/Foo_(bar)"],
    ["its own parentheses inside other ones", "(https://example.test/wiki/Foo_(bar))", "https://example.test/wiki/Foo_(bar)"],
    [
      "quotes, tildes and asterisks inside it",
      "Link: https://example.test/home#q=(id~'A~query~'sum*28x*29~range~(start~'PT12H)) ok",
      "https://example.test/home#q=(id~'A~query~'sum*28x*29~range~(start~'PT12H))",
    ],
    ["a quote closing it", "'https://example.test/x'", "https://example.test/x"],
    ["a double quote ending it", '"https://example.test/x"', "https://example.test/x"],
    ["a backtick ending it", "https://example.test/x`", "https://example.test/x"],
    ["emphasis closing it", "*https://example.test/x*", "https://example.test/x"],
    ["a loop channel link", "open loop://channel/abc123.", "loop://channel/abc123"],
  ])("finds %s", (_name, text, want) => {
    expect(find(text)).toBe(want);
  });
});
