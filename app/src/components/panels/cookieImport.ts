import type { CookieCategory, CookieDomain } from "../../api/loopApi";

/** Badge text per category; "" is an ordinary site and gets no badge. */
export const CATEGORY_LABELS: Record<CookieCategory, string> = {
  "": "",
  email: "Email",
  signin: "Sign-in provider",
  bank: "Bank or payments",
  sensitive: "Sensitive",
};

/**
 * The default tick state: ordinary sites on, classified ones off.
 *
 * A mailbox, an identity provider or a bank is never handed over by a
 * default — reaching one always costs a deliberate click. A saved selection
 * overrides this wholesale, including any classified sites the user
 * previously chose on purpose.
 */
export function defaultSelection(domains: CookieDomain[], saved?: string[]): Set<string> {
  if (saved && saved.length > 0) {
    const available = new Set(domains.map((d) => d.domain));
    return new Set(saved.filter((d) => available.has(d)));
  }
  return new Set(domains.filter((d) => d.category === "").map((d) => d.domain));
}

/** Substring match on the domain; a blank query matches everything. */
export function filterDomains(domains: CookieDomain[], query: string): CookieDomain[] {
  const q = query.trim().toLowerCase();
  if (!q) return domains;
  return domains.filter((d) => d.domain.includes(q));
}

/** Tri-state for the "Select all" checkbox. */
export function selectAllState(domains: CookieDomain[], selected: Set<string>): "none" | "some" | "all" {
  const ordinary = domains.filter((d) => d.category === "");
  if (ordinary.length === 0) return selected.size > 0 ? "some" : "none";
  const picked = ordinary.filter((d) => selected.has(d.domain)).length;
  if (picked === 0) return selected.size > 0 ? "some" : "none";
  return picked === ordinary.length ? "all" : "some";
}

/**
 * Toggle every ordinary site at once.
 *
 * "Select all" only ever reaches the unclassified rows — that is the whole
 * point of classifying them. Clearing, on the other hand, clears everything,
 * because a user reaching for "none" means none.
 */
export function toggleAll(domains: CookieDomain[], selected: Set<string>): Set<string> {
  if (selectAllState(domains, selected) === "all") return new Set();
  const next = new Set(selected);
  for (const d of domains) {
    if (d.category === "") next.add(d.domain);
  }
  return next;
}

/** A parent scope with the scopes that sit under it. */
export interface DomainGroup {
  /** The scope the members sit under. Always a real scope in this jar. */
  root: string;
  /** The scopes in the group, the root first when it holds cookies itself. */
  members: CookieDomain[];
  /** Cookies across the whole group. */
  count: number;
  /** The root's category, or a member's when the root is unclassified. */
  category: CookieCategory;
}

/**
 * Fold scopes under the parent they sit beneath.
 *
 * A site's session usually lives on the parent scope: ticking the
 * subdomain an app is served from imports that app's own cookies and none
 * of the ones that sign you in. Grouping puts that one click within reach
 * without widening what a tick grants — every member is still an exact
 * scope, and the import sends the members, not the group.
 *
 * The parent is only ever another scope from the same jar, so no public
 * suffix list is needed and no group can be invented: a site whose parent
 * is a public suffix stands alone, because nothing in the jar sets cookies
 * there, which is a thing browsers refuse anyway.
 */
export function groupDomains(domains: CookieDomain[]): DomainGroup[] {
  const present = new Set(domains.map((d) => d.domain));
  const groups = new Map<string, DomainGroup>();

  for (const d of domains) {
    const root = rootScope(d.domain, present);
    const group = groups.get(root);
    if (!group) {
      groups.set(root, { root, members: [d], count: d.count, category: d.domain === root ? d.category : "" });
      continue;
    }
    // The root's own row carries the group's identity, wherever it turns up
    // in the incoming order.
    if (d.domain === root) {
      group.members.unshift(d);
      group.category = d.category;
    } else {
      group.members.push(d);
      if (group.category === "") group.category = d.category;
    }
    group.count += d.count;
  }
  return [...groups.values()];
}

/** The shortest scope in the jar that this one sits under, or itself. */
function rootScope(domain: string, present: Set<string>): string {
  const labels = domain.split(".");
  for (let i = labels.length - 2; i >= 1; i--) {
    const parent = labels.slice(i).join(".");
    if (present.has(parent)) return parent;
  }
  return domain;
}

/**
 * Keep the groups a query touches.
 *
 * Matching the parent keeps the whole group — someone typing a site's name
 * wants all of it — while matching a member narrows the group to what
 * matched.
 */
export function filterGroups(groups: DomainGroup[], query: string): DomainGroup[] {
  const q = query.trim().toLowerCase();
  if (!q) return groups;

  const out: DomainGroup[] = [];
  for (const g of groups) {
    if (g.root.includes(q)) {
      out.push(g);
      continue;
    }
    const members = g.members.filter((m) => m.domain.includes(q));
    if (members.length > 0) {
      out.push({ ...g, members, count: members.reduce((n, m) => n + m.count, 0) });
    }
  }
  return out;
}

/** Tri-state for a group's own checkbox. */
export function groupState(group: DomainGroup, selected: Set<string>): "none" | "some" | "all" {
  const picked = group.members.filter((m) => selected.has(m.domain)).length;
  if (picked === 0) return "none";
  return picked === group.members.length ? "all" : "some";
}

/**
 * Toggle a whole group.
 *
 * Ticking reaches the classified members too: the row is badged and the
 * click is deliberate, which is the same bar a classified row on its own
 * has to clear.
 */
export function toggleGroup(group: DomainGroup, selected: Set<string>): Set<string> {
  const next = new Set(selected);
  const clearing = groupState(group, selected) === "all";
  for (const m of group.members) {
    if (clearing) next.delete(m.domain);
    else next.add(m.domain);
  }
  return next;
}
