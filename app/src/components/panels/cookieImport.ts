import type { CookieDomain } from "../../api/loopApi";

/**
 * The default tick state: nothing.
 *
 * Nothing is handed to an agent that the user did not pick out by name. The
 * picker used to tick everything it did not recognise as a mailbox, a bank
 * or an identity provider, which meant every site no list had heard of went
 * over by default — an open-ended set no list can close. Starting empty
 * closes it: what is unrecognised is simply not selected. A saved selection
 * overrides this wholesale.
 */
export function defaultSelection(domains: CookieDomain[], saved?: string[]): Set<string> {
  if (saved && saved.length > 0) {
    const available = new Set(domains.map((d) => d.domain));
    return new Set(saved.filter((d) => available.has(d)));
  }
  return new Set();
}

/** Substring match on the domain; a blank query matches everything. */
export function filterDomains(domains: CookieDomain[], query: string): CookieDomain[] {
  const q = query.trim().toLowerCase();
  if (!q) return domains;
  return domains.filter((d) => d.domain.includes(q));
}

/** Tri-state for the "Select all" checkbox. */
export function selectAllState(domains: CookieDomain[], selected: Set<string>): "none" | "some" | "all" {
  if (domains.length === 0) return selected.size > 0 ? "some" : "none";
  const picked = domains.filter((d) => selected.has(d.domain)).length;
  if (picked === 0) return selected.size > 0 ? "some" : "none";
  return picked === domains.length ? "all" : "some";
}

/**
 * Toggle every site at once.
 *
 * "Select all" reaches every row, because there is no longer a class of row
 * it steps around: what it grants is exactly what the list shows, and the
 * user asked for it by name.
 */
export function toggleAll(domains: CookieDomain[], selected: Set<string>): Set<string> {
  if (selectAllState(domains, selected) === "all") return new Set();
  const next = new Set(selected);
  for (const d of domains) next.add(d.domain);
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
      groups.set(root, { root, members: [d], count: d.count });
      continue;
    }
    // The root's own row comes first, wherever it turns up in the incoming
    // order.
    if (d.domain === root) {
      group.members.unshift(d);
    } else {
      group.members.push(d);
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
 * The group is a convenience over its members and nothing more: ticking it
 * selects exactly the scopes listed under it, one grant each.
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
