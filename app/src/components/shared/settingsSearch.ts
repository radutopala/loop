import type { ConfigSchema, SchemaProperty } from "../../api/configApi";
import { schemaToFields } from "./ConfigForm";

/** A config key that matched a settings search, under its sidebar section. */
export interface SettingsKeyMatch {
  /** The form field to jump to (a leaf, or the list/map holding the key). */
  field: string;
  /** The matched key's full path; list and map entries show as [] and *. */
  path: string;
}

/** Search results by sidebar section, in sidebar order. */
export type SettingsSearchResult = Map<string, SettingsKeyMatch[]>;

/** normalize lowercases and turns key separators (. _ - [] *) into spaces,
 *  so "dictation model" finds dictation.model. */
function normalize(s: string): string {
  return s
    .toLowerCase()
    .replace(/[._\-[\]*\s]+/g, " ")
    .trim();
}

function matches(query: string[], ...texts: (string | undefined)[]): boolean {
  const hay = normalize(texts.filter(Boolean).join(" "));
  return query.every((t) => hay.includes(t));
}

/** itemPaths lists the keys inside a list's or map's entries, recursively. */
function itemPaths(props: Record<string, SchemaProperty>, prefix: string): { path: string; title?: string }[] {
  const out: { path: string; title?: string }[] = [];
  for (const [key, prop] of Object.entries(props)) {
    const path = `${prefix}.${key}`;
    out.push({ path, title: prop.title });
    const nested = prop.properties ?? prop.items?.properties ?? prop.additionalProperties?.properties;
    if (nested) {
      const sep = prop.properties ? "" : prop.items ? "[]" : ".*";
      out.push(...itemPaths(nested, path + sep));
    }
  }
  return out;
}

/** searchSettings finds the sections whose name or any key inside them —
 *  nested object keys and the keys of list and map entries included —
 *  matches query. sections are the sidebar's names in order; ones without
 *  schema fields (Daemon, JSON, …) match by name only. Returns null for an
 *  empty query. */
export function searchSettings(schema: ConfigSchema | null, isGlobal: boolean, sections: string[], query: string): SettingsSearchResult | null {
  const q = normalize(query).split(" ").filter(Boolean);
  if (q.length === 0) return null;

  const bySection = new Map<string, SettingsKeyMatch[]>();
  for (const f of schema ? schemaToFields(schema, isGlobal) : []) {
    const hits = bySection.get(f.section) ?? [];
    if (matches(q, f.key, f.label)) hits.push({ field: f.key, path: f.key });
    if (f.itemProperties) {
      const sep = f.type === "objectarray" ? "[]" : ".*";
      for (const item of itemPaths(f.itemProperties, f.key + sep)) {
        if (matches(q, item.path, item.title)) hits.push({ field: f.key, path: item.path });
      }
    }
    bySection.set(f.section, hits);
  }

  const result: SettingsSearchResult = new Map();
  for (const name of sections) {
    const hits = bySection.get(name) ?? [];
    if (hits.length > 0 || matches(q, name)) result.set(name, hits);
  }
  return result;
}
