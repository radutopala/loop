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
