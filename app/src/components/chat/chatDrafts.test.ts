// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from "vitest";
import { draftText } from "./chatDrafts";

describe("draftText", () => {
  beforeEach(() => localStorage.clear());

  it("keeps one draft per channel across reads", () => {
    draftText.set("a", "hello");
    draftText.set("b", "world");
    expect(draftText.get("a")).toBe("hello");
    expect(draftText.get("b")).toBe("world");
    expect(JSON.parse(localStorage.getItem("loop-chat-drafts")!)).toEqual({ a: "hello", b: "world" });
  });

  it("has no draft when nothing is stored", () => {
    expect(draftText.get("a")).toBeUndefined();
  });

  it("deletes only the given channel's draft", () => {
    draftText.set("a", "hello");
    draftText.set("b", "world");
    draftText.delete("a");
    expect(draftText.get("a")).toBeUndefined();
    expect(draftText.get("b")).toBe("world");
  });

  it("deletes from empty storage without failing", () => {
    draftText.delete("a");
    expect(JSON.parse(localStorage.getItem("loop-chat-drafts")!)).toEqual({});
  });
});
