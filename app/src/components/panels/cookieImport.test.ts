import { describe, expect, it } from "vitest";
import type { CookieDomain } from "../../api/loopApi";
import { defaultSelection, filterDomains, filterGroups, groupDomains, groupState, selectAllState, toggleAll, toggleGroup } from "./cookieImport";

const stripe: CookieDomain = { domain: "stripe.com", count: 3 };

const domains: CookieDomain[] = [
  { domain: "mail.example.com", count: 12 },
  stripe,
  { domain: "example.okta.com", count: 2 },
  { domain: "my-bank.example", count: 1 },
  { domain: "example.com", count: 8 },
  { domain: "docs.example.org", count: 4 },
];

const allDomains = new Set(domains.map((d) => d.domain));

describe("defaultSelection", () => {
  // The picker hands over nothing it was not told to. Anything else would
  // mean guessing which of a stranger's sessions are safe to give away.
  it("starts with nothing checked", () => {
    expect(defaultSelection(domains)).toEqual(new Set());
  });

  it("restores a saved selection", () => {
    expect(defaultSelection(domains, ["stripe.com", "example.com"])).toEqual(new Set(["stripe.com", "example.com"]));
  });

  // Selections are per profile and profiles drift: a site that is no longer
  // in this jar cannot be imported from it.
  it("drops saved sites that are not in this profile", () => {
    expect(defaultSelection(domains, ["gone.example"])).toEqual(new Set());
  });

  it("stays empty when the saved selection is empty", () => {
    expect(defaultSelection(domains, [])).toEqual(new Set());
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
  it("is all when every site is checked", () => {
    expect(selectAllState(domains, allDomains)).toBe("all");
  });

  it("is some when only part of them is", () => {
    expect(selectAllState(domains, new Set(["example.com"]))).toBe("some");
  });

  // An empty list still has to report that something is ticked, or the box
  // lies about the import.
  it("is some when the list is empty but something is selected", () => {
    expect(selectAllState([], new Set(["stripe.com"]))).toBe("some");
    expect(selectAllState([stripe], new Set(["elsewhere.example"]))).toBe("some");
  });

  it("is none when nothing is checked", () => {
    expect(selectAllState(domains, new Set())).toBe("none");
    expect(selectAllState([], new Set())).toBe("none");
  });
});

describe("toggleAll", () => {
  it("reaches every row", () => {
    expect(toggleAll(domains, new Set())).toEqual(allDomains);
  });

  it("keeps what was already picked", () => {
    expect(toggleAll([stripe], new Set(["other.example"]))).toEqual(new Set(["other.example", "stripe.com"]));
  });

  it("clears everything when all sites are already checked", () => {
    expect(toggleAll(domains, allDomains)).toEqual(new Set());
  });
});

// A login spread across a parent scope and the subdomains under it, plus a
// site whose parent is a public suffix nobody sets cookies on.
const spread: CookieDomain[] = [
  { domain: "example.com", count: 20 },
  { domain: "mail.example.com", count: 10 },
  { domain: "accounts.example.com", count: 6 },
  { domain: "access.workspace.example.com", count: 3 },
  { domain: "example.co.uk", count: 4 },
  { domain: "example.net", count: 2 },
  { domain: "login.example.net", count: 1 },
];

describe("groupDomains", () => {
  it("folds every scope under the parent that is itself in the jar", () => {
    expect(groupDomains(spread).map((g) => [g.root, g.members.map((m) => m.domain)])).toEqual([
      ["example.com", ["example.com", "mail.example.com", "accounts.example.com", "access.workspace.example.com"]],
      ["example.co.uk", ["example.co.uk"]],
      ["example.net", ["example.net", "login.example.net"]],
    ]);
  });

  it("sums the cookies across the group", () => {
    expect(groupDomains(spread)[0]?.count).toBe(39);
  });

  // No public suffix list is consulted, so no group can be invented for a
  // scope no browser would accept a cookie on.
  it("leaves a site alone when its parent sets no cookies", () => {
    expect(groupDomains(spread)[1]).toEqual({
      root: "example.co.uk",
      members: [{ domain: "example.co.uk", count: 4 }],
      count: 4,
    });
  });

  // The parent can arrive after its children when it holds fewer cookies.
  it("puts the root first however late it turns up", () => {
    const g = groupDomains([
      { domain: "mail.example.com", count: 9 },
      { domain: "example.com", count: 1 },
    ])[0];
    expect(g?.root).toBe("example.com");
    expect(g?.members.map((m) => m.domain)).toEqual(["example.com", "mail.example.com"]);
  });
});

describe("filterGroups", () => {
  it("keeps the whole group when the parent matches", () => {
    const [g] = filterGroups(groupDomains(spread), "EXAMPLE.com ");
    expect(g?.members).toHaveLength(4);
  });

  it("narrows the group to the members that match", () => {
    const got = filterGroups(groupDomains(spread), "accounts");
    expect(got).toHaveLength(1);
    expect(got[0]?.members.map((m) => m.domain)).toEqual(["accounts.example.com"]);
    expect(got[0]?.count).toBe(6);
  });

  it("returns everything for a blank query and nothing for a miss", () => {
    expect(filterGroups(groupDomains(spread), "  ")).toHaveLength(3);
    expect(filterGroups(groupDomains(spread), "nope")).toEqual([]);
  });
});

describe("groupState and toggleGroup", () => {
  const [g] = groupDomains(spread);

  it("reports none, some and all across the members", () => {
    if (!g) throw new Error("no group");
    expect(groupState(g, new Set())).toBe("none");
    expect(groupState(g, new Set(["mail.example.com"]))).toBe("some");
    expect(groupState(g, new Set(g.members.map((m) => m.domain)))).toBe("all");
  });

  // One click has to reach every member, or the group cannot do the job it
  // exists for: signing you in.
  it("ticks every member", () => {
    if (!g) throw new Error("no group");
    expect(toggleGroup(g, new Set(["other.example"]))).toEqual(new Set(["other.example", "example.com", "mail.example.com", "accounts.example.com", "access.workspace.example.com"]));
  });

  it("clears the group without touching anything else", () => {
    if (!g) throw new Error("no group");
    const all = new Set([...g.members.map((m) => m.domain), "other.example"]);
    expect(toggleGroup(g, all)).toEqual(new Set(["other.example"]));
  });
});
