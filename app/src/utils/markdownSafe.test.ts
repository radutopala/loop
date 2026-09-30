// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderMarkdownSafe } from "./markdownSafe";

// The first link in the rendered markdown.
function firstLink(md: string): HTMLAnchorElement {
  const div = document.createElement("div");
  div.innerHTML = renderMarkdownSafe(md);
  const a = div.querySelector("a");
  if (!a) throw new Error(`no link rendered for ${md}`);
  return a;
}

describe("renderMarkdownSafe", () => {
  it("renders markdown", () => {
    expect(renderMarkdownSafe("# Title\n\n**bold**")).toContain("<h1>Title</h1>");
    expect(renderMarkdownSafe("**bold**")).toContain("<strong>bold</strong>");
  });

  it.each([
    ["script tag", "<script>alert(1)</script>", "<script"],
    ["onerror", '<img src="https://example.com/x.png" onerror="alert(1)">', "onerror"],
    ["onclick", '<a href="https://example.com" onclick="alert(1)">x</a>', "onclick"],
    ["onload", '<svg onload="alert(1)"></svg>', "onload"],
    ["iframe", '<iframe src="https://example.com"></iframe>', "<iframe"],
    ["javascript in raw html", '<a href="javascript:alert(1)">x</a>', "javascript:"],
  ])("strips %s", (_name, md, banned) => {
    expect(renderMarkdownSafe(md).toLowerCase()).not.toContain(banned);
  });

  it.each([
    ["javascript", "[x](javascript:alert(1))"],
    ["file", "[x](file:///etc/passwd)"],
    ["data", "[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)"],
    ["relative", "[x](../other.md)"],
    ["raw html vbscript", '<a href="vbscript:msgbox(1)">x</a>'],
  ])("drops a %s href", (_name, md) => {
    const html = renderMarkdownSafe(md);
    const div = document.createElement("div");
    div.innerHTML = html;
    for (const a of div.querySelectorAll("a")) expect(a.hasAttribute("href")).toBe(false);
  });

  it.each([
    ["http", "[x](http://example.com/a)", "http://example.com/a"],
    ["https", "[x](https://example.com/a?b=1)", "https://example.com/a?b=1"],
    ["mailto", "[x](mailto:someone@example.com)", "mailto:someone@example.com"],
    ["autolink", "<https://example.com>", "https://example.com"],
    ["other loop link", "[x](loop://settings)", "loop://settings"],
  ])("keeps a %s href, opening outside the page", (_name, md, href) => {
    const a = firstLink(md);
    expect(a.getAttribute("href")).toBe(href);
    expect(a.getAttribute("target")).toBe("_blank");
    expect(a.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("overrides a raw html link's own target and rel", () => {
    const a = firstLink('<a href="https://example.com" target="_self" rel="opener">x</a>');
    expect(a.getAttribute("target")).toBe("_blank");
    expect(a.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it.each([
    ["channel", "[x](loop://channel/abc)", "#abc"],
    ["message", "[x](loop://channel/abc/42)", "#abc/42"],
  ])("turns a loop %s link into an in-app link", (_name, md, href) => {
    const a = firstLink(md);
    expect(a.getAttribute("href")).toBe(href);
    expect(a.hasAttribute("target")).toBe(false);
    expect(a.hasAttribute("rel")).toBe(false);
  });

  it("keeps images from allowed sources", () => {
    expect(renderMarkdownSafe("![alt](https://example.com/x.png)")).toContain('src="https://example.com/x.png"');
    expect(renderMarkdownSafe("![alt](javascript:alert(1))")).not.toContain("javascript:");
  });
});
