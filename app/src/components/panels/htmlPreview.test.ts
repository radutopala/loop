import { describe, expect, it } from "vitest";
import { buildRawFileBase } from "../../api/files";
import { withBaseHref } from "./htmlPreview";

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
  it("points at the file's directory on the raw endpoint", () => {
    expect(buildRawFileBase("ch-1", "site/pages/index.html", 1)).toMatch(/\/api\/channels\/ch-1\/raw\/1\/site\/pages\/$/);
  });

  it("uses root 0 and the root dir for top-level files", () => {
    expect(buildRawFileBase("ch-1", "index.html")).toMatch(/\/api\/channels\/ch-1\/raw\/0\/$/);
  });

  it("encodes path segments", () => {
    expect(buildRawFileBase("ch-1", "my docs/#1/a.html")).toMatch(/\/raw\/0\/my%20docs\/%231\/$/);
  });
});
