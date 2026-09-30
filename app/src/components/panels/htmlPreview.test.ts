import { describe, expect, it, vi } from "vitest";
import type { ContentCapScope } from "../../api/api";
import { buildFileUrl, buildRawFileBase } from "../../api/files";
import { withBaseHref } from "./htmlPreview";

vi.mock("../../api/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/api")>()),
  contentCapBase: async (scope: ContentCapScope) => (scope.kind === "raw" ? `http://api/c/raw:${scope.channelId}:${scope.root}/` : "http://api/c/pg/"),
}));

const BASE = "http://localhost:8222/api/channels/ch/raw/0/site/";

describe("withBaseHref", () => {
  it("injects the base at the top of <head>", () => {
    const out = withBaseHref('<html><head lang="en"><title>t</title></head></html>', BASE);
    expect(out.startsWith(`<html><head lang="en"><base href="${BASE}"><script>`)).toBe(true);
    expect(out.endsWith("</script><title>t</title></head></html>")).toBe(true);
  });

  it("falls back to just after <html> without a <head>", () => {
    const out = withBaseHref("<HTML><body>x</body></HTML>", BASE);
    expect(out.startsWith(`<HTML><base href="${BASE}"><script>`)).toBe(true);
    expect(out.endsWith("</script><body>x</body></HTML>")).toBe(true);
  });

  it("prepends to a fragment", () => {
    const out = withBaseHref("<p>hi</p>", BASE);
    expect(out.startsWith(`<base href="${BASE}"><script>`)).toBe(true);
    expect(out.endsWith("</script><p>hi</p>")).toBe(true);
  });

  it("keeps the page's own <base> and still adds the hash-link handler", () => {
    const out = withBaseHref('<head><base href="https://example.com/"></head>', BASE);
    expect(out).not.toContain(BASE);
    expect(out).toContain('<base href="https://example.com/">');
    expect(out).toContain("<script>");
  });

  it("does not mistake <header> for <head>", () => {
    const out = withBaseHref("<html><header>h</header></html>", BASE);
    expect(out.startsWith(`<html><base href="${BASE}">`)).toBe(true);
  });

  it("escapes the base href", () => {
    expect(withBaseHref("", 'http://x/a"b&c/')).toContain('<base href="http://x/a&quot;b&amp;c/">');
  });
});

describe("buildRawFileBase", () => {
  it("points at the file's directory under the root's content link", async () => {
    await expect(buildRawFileBase("ch-1", "site/pages/index.html", 1)).resolves.toBe("http://api/c/raw:ch-1:1/site/pages/");
  });

  it("uses root 0 and the link itself for top-level files", async () => {
    await expect(buildRawFileBase("ch-1", "index.html")).resolves.toBe("http://api/c/raw:ch-1:0/");
  });

  it("encodes path segments", async () => {
    await expect(buildRawFileBase("ch-1", "my docs/#1/a.html")).resolves.toBe("http://api/c/raw:ch-1:0/my%20docs/%231/");
  });
});

describe("buildFileUrl", () => {
  it("appends the encoded path and the cache buster", async () => {
    await expect(buildFileUrl("ch-1", "img/a b.png", 2, 7)).resolves.toBe("http://api/c/raw:ch-1:2/img/a%20b.png?t=7");
    await expect(buildFileUrl("ch-1", "a.png")).resolves.toBe("http://api/c/raw:ch-1:0/a.png");
  });
});
