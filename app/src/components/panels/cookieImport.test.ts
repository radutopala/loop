import { describe, expect, it } from "vitest";
import type { CookieDomain } from "../../api/loopApi";
import { CATEGORY_LABELS, defaultSelection, filterDomains, selectAllState, toggleAll } from "./cookieImport";

const stripe: CookieDomain = { domain: "stripe.com", count: 3, category: "bank" };

const domains: CookieDomain[] = [
  { domain: "mail.google.com", count: 12, category: "email" },
  stripe,
  { domain: "example.okta.com", count: 2, category: "signin" },
  { domain: "my-bank.example", count: 1, category: "sensitive" },
  { domain: "example.com", count: 8, category: "" },
  { domain: "docs.example.org", count: 4, category: "" },
];

describe("defaultSelection", () => {
  it("leaves email, bank, sign-in and user-marked sites unchecked", () => {
    expect(defaultSelection(domains)).toEqual(new Set(["example.com", "docs.example.org"]));
  });

  it("restores a saved selection, including classified sites chosen on purpose", () => {
    expect(defaultSelection(domains, ["stripe.com", "example.com"])).toEqual(new Set(["stripe.com", "example.com"]));
  });

  // Selections are per profile and profiles drift: a site that is no longer
  // in this jar cannot be imported from it.
  it("drops saved sites that are not in this profile", () => {
    expect(defaultSelection(domains, ["gone.example"])).toEqual(new Set());
  });

  it("falls back to the default when the saved selection is empty", () => {
    expect(defaultSelection(domains, [])).toEqual(new Set(["example.com", "docs.example.org"]));
  });
});

describe("filterDomains", () => {
  it("matches on a substring, case-insensitively", () => {
    expect(filterDomains(domains, " OKTA ").map((d) => d.domain)).toEqual(["example.okta.com"]);
  });

  it("returns everything for a blank query", () => {
    expect(filterDomains(domains, "   ")).toHaveLength(domains.length);
  });

  it("returns nothing when no site matches", () => {
    expect(filterDomains(domains, "nope")).toEqual([]);
  });
});

describe("selectAllState", () => {
  it("is all when every ordinary site is checked", () => {
    expect(selectAllState(domains, new Set(["example.com", "docs.example.org"]))).toBe("all");
  });

  it("is some when only part of them is", () => {
    expect(selectAllState(domains, new Set(["example.com"]))).toBe("some");
  });

  // A picker showing only classified rows still has to report that
  // something is ticked, or the box lies about the import.
  it("is some when only classified sites are checked", () => {
    expect(selectAllState(domains, new Set(["stripe.com"]))).toBe("some");
    expect(selectAllState([stripe], new Set(["stripe.com"]))).toBe("some");
  });

  it("is none when nothing is checked", () => {
    expect(selectAllState(domains, new Set())).toBe("none");
    expect(selectAllState([stripe], new Set())).toBe("none");
  });
});

describe("toggleAll", () => {
  it("only ever reaches the unclassified rows", () => {
    expect(toggleAll(domains, new Set())).toEqual(new Set(["example.com", "docs.example.org"]));
  });

  it("keeps a classified site the user picked deliberately", () => {
    expect(toggleAll(domains, new Set(["stripe.com"]))).toEqual(new Set(["stripe.com", "example.com", "docs.example.org"]));
  });

  // Reaching for "none" means none, classified sites included.
  it("clears everything when all ordinary sites are already checked", () => {
    expect(toggleAll(domains, new Set(["stripe.com", "example.com", "docs.example.org"]))).toEqual(new Set());
  });
});

describe("CATEGORY_LABELS", () => {
  it("badges every classified category and leaves ordinary sites bare", () => {
    expect(CATEGORY_LABELS).toEqual({
      "": "",
      email: "Email",
      signin: "Sign-in provider",
      bank: "Bank or payments",
      sensitive: "Sensitive",
    });
  });
});
