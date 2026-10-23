import type { SchemaProperty } from "../../api/configApi";

type Config = Record<string, any>;

/** A boolean field that adds a mounts entry when on (x-mount). */
export interface MountToggle {
  key: string;
  mount: string;
  /** Field whose value, when set, replaces the entry's host path. */
  sourceKey?: string;
}

/** Collects the schema's x-mount toggles, keyed by their dotted path. */
export function mountToggles(props: Record<string, SchemaProperty> | undefined, prefix = ""): MountToggle[] {
  const out: MountToggle[] = [];
  for (const [key, prop] of Object.entries(props ?? {})) {
    const fullKey = prefix ? `${prefix}.${key}` : key;
    if (prop["x-mount"]) out.push({ key: fullKey, mount: prop["x-mount"], sourceKey: prop["x-mount-source"] });
    if (prop.properties) out.push(...mountToggles(prop.properties, fullKey));
  }
  return out;
}

/** A mount's container path, without a trailing slash or :ro/:rw. */
export function mountTarget(mount: string): string {
  const parts = mount.split(":");
  if (parts.length > 2 && (parts.at(-1) === "ro" || parts.at(-1) === "rw")) parts.pop();
  return parts.length < 2 ? "" : (parts.at(-1) ?? "").replace(/\/+$/, "");
}

function covers(mount: string, target: string): boolean {
  const t = mountTarget(mount);
  return t === target || t.startsWith(`${target}/`);
}

function get(cfg: Config, path: string): any {
  return path.split(".").reduce<any>((o, k) => (o && typeof o === "object" ? o[k] : undefined), cfg);
}

function mountFor(t: MountToggle, cfg: Config): string {
  const src = t.sourceKey ? get(cfg, t.sourceKey) : undefined;
  return typeof src === "string" && src !== "" ? `${src}:${t.mount.split(":").pop()}` : t.mount;
}

/**
 * Keeps the mounts list in step with an x-mount toggle after `key` changed
 * from `prev` to `next`: on adds the toggle's entry unless a mount at its
 * container path (or inside it) is already there, in `next` or in
 * `inherited` (the global mounts a project inherits); off removes the
 * mounts at that path; a new source path while on swaps the entry.
 */
export function syncConfigMounts(prev: Config, next: Config, key: string, toggles: MountToggle[], inherited: string[] = []): Config {
  let mounts: string[] = Array.isArray(next.mounts) ? next.mounts : [];
  const before = mounts;
  for (const t of toggles) {
    if (key !== t.key && key !== t.sourceKey) continue;
    const target = mountTarget(t.mount);
    if (get(next, t.key) !== true) {
      if (key === t.key) mounts = mounts.filter((m) => !covers(m, target));
      continue;
    }
    const old = mountFor(t, prev);
    const want = mountFor(t, next);
    if (old !== want) mounts = mounts.filter((m) => m !== old);
    if (![...mounts, ...inherited].some((m) => covers(m, target))) mounts = [...mounts, want];
  }
  if (mounts === before) return next;
  const out = { ...next };
  if (mounts.length > 0) out.mounts = mounts;
  else delete out.mounts;
  return out;
}
