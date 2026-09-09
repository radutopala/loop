import { describe, expect, it } from "vitest";
import { normalizeNavigateUrl } from "./browserUrl";

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
