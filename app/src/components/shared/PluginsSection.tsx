import { useCallback, useEffect, useState } from "react";
import {
  fetchPluginList,
  inspectPlugin,
  installPlugin,
  PLUGINS_CHANGED,
  type PluginCandidate,
  type PluginInfo,
  type PluginList,
  removePlugin,
  setPluginEnabled,
  stopPluginContainer,
} from "../../api/plugins";
import { useEventStream } from "../../hooks/useEventStream";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { WSEvent } from "../../types";
import { PluginSettings } from "./PluginSettings";

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** The plugin list, for channelId's project when given, fetched again when
 *  plugins or their enablement change. */
function usePluginList(channelId: string | null): { list: PluginList | null; error: string | null; reload: () => void } {
  const [list, setList] = useState<PluginList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [epoch, setEpoch] = useState(0);
  const reload = useCallback(() => setEpoch((n) => n + 1), []);
  useEffect(() => {
    let cancelled = false;
    fetchPluginList(channelId)
      .then((l) => {
        if (cancelled) return;
        setList(l);
        setError(null);
      })
      .catch((e) => {
        if (!cancelled) setError(errText(e));
      });
    return () => {
      cancelled = true;
    };
  }, [channelId, epoch]);
  useEffect(() => {
    window.addEventListener(PLUGINS_CHANGED, reload);
    return () => window.removeEventListener(PLUGINS_CHANGED, reload);
  }, [reload]);
  const onEvent = useCallback(
    (ev: WSEvent) => {
      if (ev.type === "plugins.changed") reload();
    },
    [reload],
  );
  useEventStream({ channelId, onEvent });
  return { list, error, reload };
}

function buttonStyle(colors: ColorPalette, opts: { danger?: boolean; disabled?: boolean } = {}): React.CSSProperties {
  return {
    padding: "5px 10px",
    backgroundColor: opts.danger ? colors.error : colors.bg,
    border: `1px solid ${opts.danger ? colors.error : colors.border}`,
    borderRadius: 6,
    color: opts.danger ? colors.white : opts.disabled ? colors.textDim : colors.text,
    fontSize: 12,
    cursor: opts.disabled ? "default" : "pointer",
    fontFamily: "inherit",
    whiteSpace: "nowrap",
  };
}

function Note({ colors, children }: { colors: ColorPalette; children: React.ReactNode }) {
  return <div style={{ backgroundColor: colors.bg, borderRadius: 8, padding: "10px 12px", marginBottom: 12, fontSize: 12, color: colors.textDim, lineHeight: 1.5 }}>{children}</div>;
}

function Toggle({ label, checked, disabled, onChange, testId }: { label: string; checked: boolean; disabled?: boolean; onChange: (v: boolean) => void; testId: string }) {
  return (
    <label style={{ display: "flex", alignItems: "center", gap: 6, cursor: disabled ? "default" : "pointer", fontSize: 12 }}>
      <input data-testid={testId} type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return <code style={{ fontFamily: fonts.mono }}>{children}</code>;
}

/** The repository part of an image reference, without its tag or digest,
 *  as a starting point for an upgrade. */
export function imageRepo(image: string | undefined): string {
  if (!image) return "";
  const noDigest = image.split("@")[0]!;
  const slash = noDigest.lastIndexOf("/");
  const colon = noDigest.lastIndexOf(":");
  return colon > slash ? noDigest.slice(0, colon) : noDigest;
}

/** Inspects an image, shows what it asks for and installs it once the owner
 *  approves. */
function InstallFlow({ colors, initialImage, dev: initialDev, onDone, onCancel }: { colors: ColorPalette; initialImage: string; dev: boolean; onDone: () => void; onCancel?: () => void }) {
  const [image, setImage] = useState(initialImage);
  const [dev, setDev] = useState(initialDev);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [candidate, setCandidate] = useState<PluginCandidate | null>(null);

  const inspect = async () => {
    setBusy(true);
    setError(null);
    try {
      setCandidate(await inspectPlugin(image.trim(), dev));
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  const install = async () => {
    if (!candidate) return;
    setBusy(true);
    setError(null);
    try {
      await installPlugin(candidate, candidate.capabilities);
      setCandidate(null);
      setImage(initialImage);
      setDev(initialDev);
      onDone();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  if (candidate) {
    const isNew = (c: string) => candidate.new_capabilities?.includes(c);
    return (
      <div data-testid="plugin-review" style={{ border: `1px solid ${colors.border}`, borderRadius: 8, padding: 12, fontSize: 12, color: colors.text, lineHeight: 1.6 }}>
        <div style={{ fontWeight: 600 }}>
          {candidate.name || candidate.id} {candidate.previous_version ? `${candidate.previous_version} → ${candidate.version}` : candidate.version}
          {candidate.dev && <span style={{ marginLeft: 6, color: colors.warning }}>dev</span>}
        </div>
        <div style={{ color: colors.textDim, fontFamily: fonts.mono, fontSize: 11, wordBreak: "break-all" }}>{candidate.image}</div>
        <div style={{ marginTop: 8 }}>{candidate.capabilities.length ? "It asks for:" : "It asks for no capabilities."}</div>
        {candidate.capabilities.map((c) => (
          <div key={c} data-testid="plugin-review-cap" style={{ fontFamily: fonts.mono, color: isNew(c) ? colors.warning : colors.text }}>
            {c}
            {isNew(c) && candidate.previous_version ? "  (new)" : ""}
          </div>
        ))}
        {error && <div style={{ color: colors.error, marginTop: 8 }}>{error}</div>}
        <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
          <button data-testid="plugin-approve" onClick={install} disabled={busy} style={buttonStyle(colors, { disabled: busy })}>
            {busy ? "Installing…" : candidate.previous_version ? "Approve and upgrade" : "Approve and install"}
          </button>
          <button
            onClick={() => {
              setCandidate(null);
              onCancel?.();
            }}
            disabled={busy}
            style={buttonStyle(colors, { disabled: busy })}
          >
            Cancel
          </button>
        </div>
      </div>
    );
  }

  return (
    <div>
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <input
          data-testid="plugin-image"
          value={image}
          onChange={(e) => setImage(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && image.trim()) void inspect();
          }}
          placeholder="registry/name:tag"
          spellCheck={false}
          style={{
            flex: 1,
            padding: "5px 8px",
            backgroundColor: colors.bg,
            border: `1px solid ${colors.inputBorder}`,
            borderRadius: 6,
            color: colors.text,
            fontSize: 12,
            fontFamily: fonts.mono,
          }}
        />
        <Toggle label="Local dev image" checked={dev} onChange={setDev} testId="plugin-dev" />
        <button data-testid="plugin-inspect" onClick={inspect} disabled={busy || !image.trim()} style={buttonStyle(colors, { disabled: busy || !image.trim() })}>
          {busy ? "Checking…" : "Review"}
        </button>
        {onCancel && (
          <button onClick={onCancel} style={buttonStyle(colors)}>
            Cancel
          </button>
        )}
      </div>
      {error && <div style={{ color: colors.error, fontSize: 12, marginTop: 6 }}>{error}</div>}
    </div>
  );
}

function ContainerRows({ colors, plugin, only, onError }: { colors: ColorPalette; plugin: PluginInfo; only?: string; onError: (e: string) => void }) {
  const rows = (plugin.containers ?? []).filter((c) => !only || c.channel_id === only);
  if (rows.length === 0) return <div style={{ color: colors.textDim }}>No containers yet; one starts in a channel when the plugin is first used there.</div>;
  return (
    <>
      {rows.map((c) => (
        <div key={c.channel_id} data-testid="plugin-container" style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={c.last_error || c.channel_id}>
            {c.channel_name || c.channel_id}
            {c.last_error && <span style={{ color: colors.error }}> · {c.last_error}</span>}
          </span>
          <span style={{ color: c.status === "running" ? colors.active : colors.textDim }}>{c.status}</span>
          {c.status === "running" && (
            <button
              data-testid="plugin-container-stop"
              onClick={() =>
                stopPluginContainer(plugin.id, c.channel_id)
                  .then(() => window.dispatchEvent(new Event(PLUGINS_CHANGED)))
                  .catch((e) => onError(errText(e)))
              }
              style={buttonStyle(colors)}
            >
              Stop
            </button>
          )}
        </div>
      ))}
    </>
  );
}

function PluginCard({ colors, plugin, onChanged }: { colors: ColorPalette; plugin: PluginInfo; onChanged: () => void }) {
  const [upgrading, setUpgrading] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [purge, setPurge] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (f: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await f();
      onChanged();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-testid={`plugin-card-${plugin.id}`} style={{ border: `1px solid ${colors.border}`, borderRadius: 8, padding: 12, marginBottom: 12, fontSize: 12, color: colors.text, lineHeight: 1.6 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ fontWeight: 600 }}>{plugin.name || plugin.id}</span>
        <span style={{ color: colors.textDim, fontFamily: fonts.mono }}>{plugin.id}</span>
        <span style={{ color: colors.textDim }}>{plugin.version}</span>
        {plugin.dev && <span style={{ color: colors.warning }}>dev</span>}
        {plugin.status !== "installed" && <span style={{ color: colors.warning }}>{plugin.status}</span>}
        <div style={{ flex: 1 }} />
        <Toggle label="On by default" testId={`plugin-default-${plugin.id}`} checked={!!plugin.enabled_by_default} disabled={busy} onChange={(v) => run(() => setPluginEnabled(plugin.id, v))} />
      </div>
      <div style={{ color: colors.textDim, fontFamily: fonts.mono, fontSize: 11, wordBreak: "break-all" }}>{plugin.image}</div>
      <div style={{ marginTop: 6 }}>
        <span style={{ color: colors.textDim }}>Approved: </span>
        {plugin.capabilities.length ? plugin.capabilities.join(", ") : "nothing"}
      </div>
      {!!plugin.panels?.length && (
        <div>
          <span style={{ color: colors.textDim }}>Panels: </span>
          {plugin.panels.map((p) => p.label || p.id).join(", ")}
        </div>
      )}
      {!!plugin.actions?.length && (
        <div>
          <span style={{ color: colors.textDim }}>Actions: </span>
          {plugin.actions.join(", ")}
        </div>
      )}
      <PluginSettings colors={colors} plugin={plugin} onChanged={onChanged} />
      <div style={{ marginTop: 8, marginBottom: 4, color: colors.textDim }}>Containers</div>
      <ContainerRows colors={colors} plugin={plugin} onError={setError} />
      {error && <div style={{ color: colors.error, marginTop: 6 }}>{error}</div>}
      <div style={{ marginTop: 10 }}>
        {upgrading ? (
          <InstallFlow
            colors={colors}
            initialImage={imageRepo(plugin.image)}
            dev={!!plugin.dev}
            onDone={() => {
              setUpgrading(false);
              onChanged();
            }}
            onCancel={() => setUpgrading(false)}
          />
        ) : removing ? (
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <Toggle label="Also delete its data in every channel" checked={purge} onChange={setPurge} testId={`plugin-purge-${plugin.id}`} />
            <div style={{ flex: 1 }} />
            <button data-testid="plugin-remove-confirm" onClick={() => run(() => removePlugin(plugin.id, purge))} disabled={busy} style={buttonStyle(colors, { danger: true, disabled: busy })}>
              Remove {plugin.name || plugin.id}
            </button>
            <button onClick={() => setRemoving(false)} style={buttonStyle(colors)}>
              Cancel
            </button>
          </div>
        ) : (
          <div style={{ display: "flex", gap: 8 }}>
            <button data-testid="plugin-upgrade" onClick={() => setUpgrading(true)} style={buttonStyle(colors)}>
              Upgrade…
            </button>
            <button data-testid="plugin-remove" onClick={() => setRemoving(true)} style={buttonStyle(colors)}>
              Remove…
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

/** Settings → Global → Plugins: the installed plugins, installing,
 *  upgrading and removing them, and their containers. onConfigChanged runs
 *  after a toggle wrote the global config. */
export function GlobalPluginsSection({ colors, onConfigChanged }: { colors: ColorPalette; onConfigChanged: () => void }) {
  const { list, error, reload } = usePluginList(null);
  const changed = () => {
    reload();
    onConfigChanged();
  };
  return (
    <div data-testid="plugins-section" style={{ marginTop: 16 }}>
      <Note colors={colors}>
        A plugin runs in its own container, one per channel, and only in projects that enable it: <Code>plugins.&lt;id&gt;.enabled</Code> in the project's <Code>.loop/config.json</Code>, or here in
        the global config for every project that doesn't set it. Threads and worktree threads use their channel's container.
      </Note>
      {error && <div style={{ color: colors.error, fontSize: 12, marginBottom: 12 }}>{error}</div>}
      {list?.off ? (
        <Note colors={colors}>Plugins are off. Turn on Enabled above, save, and restart the daemon to install and run them.</Note>
      ) : (
        list && (
          <>
            {list.plugins.length === 0 && <div style={{ color: colors.textDim, fontSize: 12, marginBottom: 12 }}>No plugins installed.</div>}
            {list.plugins.map((p) => (
              <PluginCard key={p.id} colors={colors} plugin={p} onChanged={changed} />
            ))}
            <div style={{ fontSize: 12, color: colors.textDim, marginBottom: 6 }}>Install a plugin image</div>
            <InstallFlow colors={colors} initialImage="" dev={false} onDone={reload} />
          </>
        )
      )}
    </div>
  );
}

/** Settings → Project → Plugins: whether each installed plugin runs in
 *  this channel's project, and its container here. onConfigChanged runs
 *  after a toggle wrote the project config. */
export function ProjectPluginsSection({ colors, channelId, onConfigChanged }: { colors: ColorPalette; channelId: string; onConfigChanged: () => void }) {
  const { list, error, reload } = usePluginList(channelId);
  const [busy, setBusy] = useState<string | null>(null);
  const [toggleError, setToggleError] = useState<string | null>(null);

  const toggle = async (id: string, enabled: boolean) => {
    setBusy(id);
    setToggleError(null);
    try {
      await setPluginEnabled(id, enabled, channelId);
      reload();
      onConfigChanged();
    } catch (e) {
      setToggleError(errText(e));
    } finally {
      setBusy(null);
    }
  };

  if (error) return <div style={{ color: colors.error, fontSize: 12 }}>{error}</div>;
  if (!list) return null;
  if (list.off) return <Note colors={colors}>Plugins are off in the global config (Global → Plugins).</Note>;
  return (
    <div data-testid="project-plugins-section">
      <Note colors={colors}>
        Enabling a plugin here sets <Code>plugins.&lt;id&gt;.enabled</Code> in this project's <Code>.loop/config.json</Code>; unset, the project follows the plugin's global default. Threads and
        worktree threads use this channel's container.
      </Note>
      {list.plugins.length === 0 && <div style={{ color: colors.textDim, fontSize: 12 }}>No plugins installed. Install them under Global → Plugins.</div>}
      {toggleError && <div style={{ color: colors.error, fontSize: 12, marginBottom: 12 }}>{toggleError}</div>}
      {list.plugins.map((p) => {
        const pending = p.project_enabled !== undefined && p.project_enabled !== p.enabled;
        return (
          <div
            key={p.id}
            data-testid={`project-plugin-${p.id}`}
            style={{ border: `1px solid ${colors.border}`, borderRadius: 8, padding: 12, marginBottom: 12, fontSize: 12, color: colors.text, lineHeight: 1.6 }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ fontWeight: 600 }}>{p.name || p.id}</span>
              <span style={{ color: colors.textDim }}>{p.version}</span>
              <div style={{ flex: 1 }} />
              <Toggle label="Enabled in this project" testId={`project-plugin-enabled-${p.id}`} checked={!!p.enabled} disabled={busy === p.id} onChange={(v) => toggle(p.id, v)} />
            </div>
            <div style={{ color: colors.textDim }}>
              {p.project_enabled === undefined ? `Follows the global default (${p.enabled_by_default ? "on" : "off"}).` : `Set in this project (${p.project_enabled ? "on" : "off"}).`}
            </div>
            {pending && (
              <div data-testid={`project-plugin-pending-${p.id}`} style={{ color: colors.warning }}>
                The project config says {p.project_enabled ? "on" : "off"}, but it takes effect only once you trust the project config.
              </div>
            )}
            <PluginSettings
              colors={colors}
              plugin={p}
              channelId={channelId}
              onChanged={() => {
                reload();
                onConfigChanged();
              }}
            />
            <div style={{ marginTop: 6 }}>
              <ContainerRows colors={colors} plugin={p} only={channelId} onError={setToggleError} />
            </div>
          </div>
        );
      })}
    </div>
  );
}
