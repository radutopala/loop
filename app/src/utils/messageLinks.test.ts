import { describe, expect, it } from "vitest";
import { inAppHref, messageLink, parseChannelTarget } from "./messageLinks";

describe("messageLink", () => {
  it("links to a message in a channel", () => {
    expect(messageLink("4bc8a7f4", 382433)).toBe("loop://channel/4bc8a7f4/382433");
  });
});

describe("parseChannelTarget", () => {
  it.each([
    ["4bc8a7f4", { channelId: "4bc8a7f4", messageId: null }],
    ["#4bc8a7f4", { channelId: "4bc8a7f4", messageId: null }],
    ["4bc8a7f4/", { channelId: "4bc8a7f4", messageId: null }],
    ["4bc8a7f4/382433", { channelId: "4bc8a7f4", messageId: 382433 }],
    ["#4bc8a7f4/382433", { channelId: "4bc8a7f4", messageId: 382433 }],
    ["4bc8a7f4/0", { channelId: "4bc8a7f4", messageId: null }],
    ["C0123ABCD", { channelId: "C0123ABCD", messageId: null }],
    ["thread_ab-12", { channelId: "thread_ab-12", messageId: null }],
    ["C0123ABCD:1700000000.123456/7", { channelId: "C0123ABCD:1700000000.123456", messageId: 7 }],
  ])("parses %s", (target, want) => {
    expect(parseChannelTarget(target)).toEqual(want);
  });

  it.each([
    "",
    "#",
    "/382433",
    "4bc8a7f4/abc",
    "4bc8a7f4/-1",
    "4bc8a7f4/1.5",
    "4bc8a7f4/1/2",
    "#loop_token=abc",
    ".",
    "..",
    "#..",
    "...",
    "../382433",
    "%2e%2e",
    "4bc8?x=1",
    "4bc8#x",
    "4bc8 a",
    "4bc8\\x",
  ])("rejects %s", (target) => {
    expect(parseChannelTarget(target)).toBeNull();
  });
});

describe("inAppHref", () => {
  it("turns a message link into the page's hash", () => {
    expect(inAppHref("loop://channel/4bc8a7f4/382433")).toBe("#4bc8a7f4/382433");
  });

  it("turns a channel link into the page's hash", () => {
    expect(inAppHref("loop://channel/4bc8a7f4")).toBe("#4bc8a7f4");
  });

  it.each(["https://example.com", "loop://channel/", "loop://channel/4bc8a7f4/abc", "loop://other/4bc8a7f4"])("leaves %s alone", (url) => {
    expect(inAppHref(url)).toBeNull();
  });
});
