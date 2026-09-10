import { describe, expect, it } from "vitest";
import { normalizeNavigateUrl, safeFaviconUrl } from "./browserUrl";

describe("normalizeNavigateUrl", () => {
  it("returns null for empty or whitespace-only input", () => {
    expect(normalizeNavigateUrl("")).toBeNull();
    expect(normalizeNavigateUrl("   ")).toBeNull();
  });

  it("trims surrounding whitespace", () => {
    expect(normalizeNavigateUrl("  https://example.com  ")).toBe("https://example.com");
  });

  it("leaves http and https URLs alone, whatever the case", () => {
    expect(normalizeNavigateUrl("https://example.com/a?b=c")).toBe("https://example.com/a?b=c");
    expect(normalizeNavigateUrl("http://example.com")).toBe("http://example.com");
    expect(normalizeNavigateUrl("HTTPS://example.com")).toBe("HTTPS://example.com");
  });

  it("passes Chrome's internal pages through", () => {
    // Regression: these used to come out as "https://chrome://extensions/",
    // so the page never loaded and the pane looked broken.
    expect(normalizeNavigateUrl("chrome://extensions/")).toBe("chrome://extensions/");
    expect(normalizeNavigateUrl("chrome://version")).toBe("chrome://version");
    expect(normalizeNavigateUrl("about:blank")).toBe("about:blank");
    expect(normalizeNavigateUrl("view-source:https://example.com")).toBe("view-source:https://example.com");
    expect(normalizeNavigateUrl("chrome-extension://abc/options.html")).toBe("chrome-extension://abc/options.html");
    expect(normalizeNavigateUrl("file:///tmp/page.html")).toBe("file:///tmp/page.html");
    expect(normalizeNavigateUrl("data:text/html,<p>hi</p>")).toBe("data:text/html,<p>hi</p>");
  });

  it("prefixes a bare host with https", () => {
    expect(normalizeNavigateUrl("example.com")).toBe("https://example.com");
    expect(normalizeNavigateUrl("example.com/path")).toBe("https://example.com/path");
  });

  it("still prefixes host:port, which a generic scheme check would mistake for a scheme", () => {
    expect(normalizeNavigateUrl("localhost:3000")).toBe("https://localhost:3000");
    expect(normalizeNavigateUrl("127.0.0.1:8080/health")).toBe("https://127.0.0.1:8080/health");
  });
});

describe("safeFaviconUrl", () => {
  it("passes an inline base64 raster and trims it", () => {
    expect(safeFaviconUrl(" data:image/png;base64,iVBORw0KGgo= ")).toBe("data:image/png;base64,iVBORw0KGgo=");
    expect(safeFaviconUrl("data:image/x-icon;base64,AAABAAEAEBA=")).toBe("data:image/x-icon;base64,AAABAAEAEBA=");
    expect(safeFaviconUrl("data:image/jpeg;base64,/9j/4AAQ")).toBe("data:image/jpeg;base64,/9j/4AAQ");
    // The daemon spells the type itself, so an odd casing is not one of ours.
    expect(safeFaviconUrl("DATA:IMAGE/JPEG;BASE64,/9j/4AAQ")).toBeUndefined();
  });

  it("drops remote icons — the daemon inlines them, so a URL here means the page named a host", () => {
    expect(safeFaviconUrl("https://example.com/favicon.ico")).toBeUndefined();
    expect(safeFaviconUrl("http://example.com/icon.png")).toBeUndefined();
    expect(safeFaviconUrl("//example.com/icon.png")).toBeUndefined();
  });

  it("drops schemes a site could use to reach the renderer", () => {
    expect(safeFaviconUrl("javascript:alert(1)")).toBeUndefined();
    expect(safeFaviconUrl("  javascript:alert(1)")).toBeUndefined();
    expect(safeFaviconUrl("blob:https://example.com/abc")).toBeUndefined();
    expect(safeFaviconUrl("file:///etc/passwd")).toBeUndefined();
    expect(safeFaviconUrl("chrome://favicon/https://example.com")).toBeUndefined();
  });

  it("drops data URLs that are not base64 rasters", () => {
    // SVG carries markup, and a non-image type is not a favicon at all.
    expect(safeFaviconUrl("data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=")).toBeUndefined();
    expect(safeFaviconUrl("data:text/html;base64,PHNjcmlwdD4=")).toBeUndefined();
    expect(safeFaviconUrl("data:image/png,notbase64")).toBeUndefined();
  });

  it("drops an anchored pattern's near misses — nothing before or after counts", () => {
    expect(safeFaviconUrl("javascript:x//data:image/png;base64,iVBORw0KGgo=")).toBeUndefined();
    expect(safeFaviconUrl("data:image/png;base64,iVBORw0KGgo= trailing")).toBeUndefined();
  });

  it("returns undefined when there is no icon", () => {
    expect(safeFaviconUrl(undefined)).toBeUndefined();
    expect(safeFaviconUrl("")).toBeUndefined();
    expect(safeFaviconUrl("   ")).toBeUndefined();
  });
});
