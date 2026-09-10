import { useCallback, useEffect, useMemo, useState } from "react";
import { type CookieSource, importCookies, listCookieSources } from "../../api/loopApi";
import { useTheme } from "../../ThemeContext";
import { storageGet, storageGetJSON, storageSet } from "../../utils/storage";
import { type DomainGroup, defaultSelection, filterGroups, groupDomains, groupState, selectAllState, toggleAll, toggleGroup } from "./cookieImport";

interface CookieImportDialogProps {
  channelId: string;
  onClose: () => void;
  /** Called after a successful import, with the number of cookies installed. */
  onImported: (count: number) => void;
}

/**
 * Two-step cookie import: consent and profile choice, then the site picker.
 *
 * The selection is remembered client-side rather than written back to
 * config.json — loop's config is hand-written HJSON with comments, and a
 * daemon that rewrites it would eat them.
 */
export function CookieImportDialog({ channelId, onClose, onImported }: CookieImportDialogProps) {
  const { colors } = useTheme();
  const [step, setStep] = useState<"consent" | "sites">("consent");
  const [sources, setSources] = useState<CookieSource[] | null>(null);
  const [sourceId, setSourceId] = useState<string>(storageGet(`cookieImportSource:${channelId}`) ?? "");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const source = useMemo(() => sources?.find((s) => s.id === sourceId), [sources, sourceId]);
  const domains = useMemo(() => source?.domains ?? [], [source]);
  const groups = useMemo(() => groupDomains(domains), [domains]);
  const visible = useMemo(() => filterGroups(groups, query), [groups, query]);
  const allState = selectAllState(domains, selected);

  const loadSources = useCallback(() => {
    setBusy(true);
    setError(null);
    listCookieSources(channelId)
      .then((list) => {
        setSources(list);
        setSourceId((current) => (list.some((s) => s.id === current) ? current : (list[0]?.id ?? "")));
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false));
  }, [channelId]);

  useEffect(loadSources, [loadSources]);

  // Re-seed the tick state whenever the chosen profile changes: the saved
  // list is per profile, and a domain that is not in this jar cannot be
  // imported from it.
  useEffect(() => {
    if (!sourceId) return;
    const saved = storageGetJSON<string[]>(`cookieImportDomains:${channelId}:${sourceId}`);
    setSelected(defaultSelection(domains, saved ?? undefined));
  }, [channelId, sourceId, domains]);

  const toggle = useCallback((domain: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (!next.delete(domain)) next.add(domain);
      return next;
    });
  }, []);

  const handleImport = useCallback(() => {
    const chosen = [...selected];
    setBusy(true);
    setError(null);
    importCookies(channelId, sourceId, chosen)
      .then((res) => {
        storageSet(`cookieImportSource:${channelId}`, sourceId);
        storageSet(`cookieImportDomains:${channelId}:${sourceId}`, JSON.stringify(chosen));
        onImported(res.imported);
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false));
  }, [channelId, sourceId, selected, onImported]);

  // maxHeight is 100% of the overlay, i.e. of the browser pane — not of the
  // window. The pane is routinely shorter than the window, and a dialog
  // measured against vh spills past both its ends.
  const panelStyle: React.CSSProperties = {
    width: 520,
    maxWidth: "100%",
    maxHeight: "100%",
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    backgroundColor: colors.surface,
    border: `1px solid ${colors.border}`,
    borderRadius: 12,
    padding: 20,
    color: colors.textLight,
    fontSize: 13,
  };

  return (
    <div
      role="dialog"
      aria-label="Import cookies"
      style={{
        position: "absolute",
        inset: 0,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: "rgba(0,0,0,0.5)",
        padding: 12,
        zIndex: 20,
      }}
    >
      <div style={panelStyle}>
        <div style={{ display: "flex", flexDirection: "column", flex: "1 1 auto", minHeight: 0 }}>
          {step === "consent" ? (
            <ConsentStep colors={colors} sources={sources} sourceId={sourceId} onSelectSource={setSourceId} busy={busy} />
          ) : (
            <SiteStep
              colors={colors}
              source={source}
              visible={visible}
              selected={selected}
              total={domains.length}
              query={query}
              allState={allState}
              onQuery={setQuery}
              onToggle={toggle}
              onToggleGroup={(g) => setSelected(toggleGroup(g, selected))}
              onToggleAll={() => setSelected(toggleAll(domains, selected))}
            />
          )}
        </div>

        {error && (
          <div style={{ marginTop: 12, flexShrink: 0, color: colors.error, fontSize: 12 }} role="alert">
            {error}
          </div>
        )}

        <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 16, flexShrink: 0 }}>
          <span style={{ flex: 1, color: colors.textDim, fontSize: 12 }}>{step === "sites" ? `${selected.size} of ${domains.length} selected` : ""}</span>
          <DialogButton colors={colors} onClick={step === "consent" ? onClose : () => setStep("consent")}>
            {step === "consent" ? "Not now" : "Back"}
          </DialogButton>
          <DialogButton colors={colors} primary disabled={busy || (step === "consent" ? !source : selected.size === 0)} onClick={step === "consent" ? () => setStep("sites") : handleImport}>
            {step === "consent" ? "Choose sites" : "Import"}
          </DialogButton>
        </div>
      </div>
    </div>
  );
}

/* ---------- steps ---------- */

type Colors = ReturnType<typeof useTheme>["colors"];

function ConsentStep({ colors, sources, sourceId, onSelectSource, busy }: { colors: Colors; sources: CookieSource[] | null; sourceId: string; onSelectSource: (id: string) => void; busy: boolean }) {
  return (
    <>
      <h2 style={{ margin: 0, fontSize: 16 }}>Stay signed in to your sites</h2>
      <p style={{ margin: "8px 0 12px", color: colors.textDim, fontSize: 12, lineHeight: 1.5 }}>Import cookies from your browser so the pages this channel's browser opens are already signed in.</p>
      <ul style={{ margin: "0 0 16px", paddingLeft: 18, color: colors.textDim, fontSize: 12, lineHeight: 1.8 }}>
        <li>Cookies only. Passwords aren't imported.</li>
        <li>You choose the sites.</li>
        <li>Nothing leaves this computer.</li>
      </ul>

      {busy && !sources && <div style={{ color: colors.textDim, fontSize: 12 }}>Looking for browsers…</div>}
      {sources?.length === 0 && <div style={{ color: colors.textDim, fontSize: 12 }}>No Chrome, Edge or Firefox profile found on this machine.</div>}
      {sources && sources.length > 0 && (
        <label style={{ display: "flex", flexDirection: "column", gap: 6, fontSize: 12 }}>
          <span style={{ color: colors.textDim }}>Browser profile</span>
          <select
            value={sourceId}
            aria-label="Browser profile"
            onChange={(e) => onSelectSource(e.target.value)}
            style={{
              padding: "6px 8px",
              backgroundColor: colors.bg,
              color: colors.textLight,
              border: `1px solid ${colors.border}`,
              borderRadius: 6,
            }}
          >
            {sources.map((s) => (
              <option key={s.id} value={s.id}>
                {browserLabel(s.browser)} — {s.name}
                {s.error ? " (unreadable)" : ""}
              </option>
            ))}
          </select>
        </label>
      )}
    </>
  );
}

function SiteStep({
  colors,
  source,
  visible,
  selected,
  total,
  query,
  allState,
  onQuery,
  onToggle,
  onToggleGroup,
  onToggleAll,
}: {
  colors: Colors;
  source: CookieSource | undefined;
  visible: DomainGroup[];
  selected: Set<string>;
  total: number;
  query: string;
  allState: "none" | "some" | "all";
  onQuery: (q: string) => void;
  onToggle: (domain: string) => void;
  onToggleGroup: (group: DomainGroup) => void;
  onToggleAll: () => void;
}) {
  // A filtered list is already narrow, so it expands on its own; otherwise
  // groups stay folded, which is what turns 889 rows into something a
  // person can read.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const toggleExpanded = (root: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(root)) next.add(root);
      return next;
    });

  return (
    <>
      <h2 style={{ margin: 0, flexShrink: 0, fontSize: 16 }}>Choose sites to bring over</h2>
      <p style={{ margin: "6px 0 12px", flexShrink: 0, color: colors.textDim, fontSize: 12 }}>
        Sites with cookies in your {browserLabel(source?.browser ?? "")} profile “{source?.name ?? ""}”.
      </p>

      <input
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        placeholder="Filter sites"
        aria-label="Filter sites"
        style={{
          padding: "6px 10px",
          marginBottom: 10,
          flexShrink: 0,
          backgroundColor: colors.bg,
          color: colors.textLight,
          border: `1px solid ${colors.border}`,
          borderRadius: 6,
          fontSize: 12,
          outline: "none",
        }}
      />

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          paddingBottom: 8,
          flexShrink: 0,
          borderBottom: `1px solid ${colors.border}`,
        }}
      >
        <input
          type="checkbox"
          checked={allState === "all"}
          ref={(el) => {
            if (el) el.indeterminate = allState === "some";
          }}
          onChange={onToggleAll}
          aria-label="Select all"
        />
        <span>Select all</span>
        <span style={{ flex: 1, textAlign: "right", color: colors.textDim, fontSize: 11 }}>Nothing is imported until you pick it</span>
      </div>

      <div style={{ flex: "1 1 auto", overflowY: "auto", minHeight: 0 }}>
        {visible.length === 0 && <div style={{ padding: "12px 0", color: colors.textDim, fontSize: 12 }}>{total === 0 ? "No cookies in this profile." : "No sites match that filter."}</div>}
        {visible.map((g) => (
          <GroupRow
            key={g.root}
            colors={colors}
            group={g}
            selected={selected}
            expanded={expanded.has(g.root) || query.trim() !== ""}
            onExpand={() => toggleExpanded(g.root)}
            onToggle={onToggle}
            onToggleGroup={onToggleGroup}
          />
        ))}
      </div>
    </>
  );
}

/**
 * One parent scope and, when expanded, the scopes under it.
 *
 * The parent checkbox covers every member, because a site's session is
 * usually split across them — the cookies that sign you in sit on the parent
 * scope, not on the subdomain its app is served from. A group of one renders
 * as a plain row.
 */
function GroupRow({
  colors,
  group,
  selected,
  expanded,
  onExpand,
  onToggle,
  onToggleGroup,
}: {
  colors: Colors;
  group: DomainGroup;
  selected: Set<string>;
  expanded: boolean;
  onExpand: () => void;
  onToggle: (domain: string) => void;
  onToggleGroup: (group: DomainGroup) => void;
}) {
  const state = groupState(group, selected);
  const children = group.members.filter((m) => m.domain !== group.root);

  if (children.length === 0) {
    const only = group.members[0];
    return only ? <DomainRow domain={only} checked={selected.has(only.domain)} onToggle={() => onToggle(only.domain)} /> : null;
  }

  return (
    <>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "5px 0" }}>
        <input
          type="checkbox"
          checked={state === "all"}
          ref={(el) => {
            if (el) el.indeterminate = state === "some";
          }}
          onChange={() => onToggleGroup(group)}
          aria-label={`${group.root} and ${children.length} sites under it`}
        />
        <button
          type="button"
          onClick={onExpand}
          title={`${group.count} cookie${group.count === 1 ? "" : "s"} across ${group.members.length} sites`}
          style={{ flex: 1, display: "flex", alignItems: "center", gap: 6, padding: 0, border: "none", background: "none", color: "inherit", font: "inherit", cursor: "pointer", textAlign: "left" }}
        >
          <span style={{ width: 8, color: colors.textDim, fontSize: 9 }}>{expanded ? "▼" : "▶"}</span>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{group.root}</span>
          <span style={{ color: colors.textDim, fontSize: 11 }}>+{children.length}</span>
        </button>
      </div>
      {expanded && group.members.map((m) => <DomainRow key={m.domain} domain={m} checked={selected.has(m.domain)} onToggle={() => onToggle(m.domain)} indent />)}
    </>
  );
}

function DomainRow({ domain, checked, onToggle, indent }: { domain: { domain: string; count: number }; checked: boolean; onToggle: () => void; indent?: boolean }) {
  return (
    <label
      title={`${domain.count} cookie${domain.count === 1 ? "" : "s"}`}
      style={{ display: "flex", alignItems: "center", gap: 8, padding: "5px 0", paddingLeft: indent ? 22 : 0, cursor: "pointer" }}
    >
      <input type="checkbox" checked={checked} onChange={onToggle} />
      <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{domain.domain}</span>
    </label>
  );
}

/* ---------- bits ---------- */

function browserLabel(browser: string): string {
  switch (browser) {
    case "chrome":
      return "Google Chrome";
    case "edge":
      return "Microsoft Edge";
    case "firefox":
      return "Firefox";
    default:
      return browser;
  }
}

function DialogButton({ colors, children, onClick, primary, disabled }: { colors: Colors; children: React.ReactNode; onClick: () => void; primary?: boolean; disabled?: boolean }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        padding: "5px 14px",
        borderRadius: 8,
        border: `1px solid ${colors.border}`,
        backgroundColor: primary ? colors.textLight : "transparent",
        color: primary ? colors.bg : colors.textLight,
        cursor: disabled ? "default" : "pointer",
        opacity: disabled ? 0.5 : 1,
        fontSize: 12,
      }}
    >
      {children}
    </button>
  );
}
