import { useEffect, useState } from "react";
import type { SchemaProperty } from "../../api/configApi";
import { type PluginInfo, setPluginSetting } from "../../api/plugins";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";

/** The plugin's settings, in the order their x-order then names give. */
export function settingKeys(schema: Record<string, SchemaProperty> | undefined): string[] {
  return Object.keys(schema ?? {}).sort((a, b) => (schema![a]!["x-order"] ?? 0) - (schema![b]!["x-order"] ?? 0) || a.localeCompare(b));
}

/** The value text typed for a setting of type prop.type: undefined for an
 *  empty text, which unsets it; an error for a number that isn't one. */
export function parseSetting(prop: SchemaProperty, text: string): { value: unknown } | { error: string } {
  if (text === "") return { value: undefined };
  if (prop.type !== "integer" && prop.type !== "number") return { value: text };
  const n = Number(text);
  if (!Number.isFinite(n) || (prop.type === "integer" && !Number.isInteger(n))) return { error: `"${text}" isn't ${prop.type === "integer" ? "a whole number" : "a number"}` };
  return { value: n };
}

/** A value as the inputs show it. */
export function settingText(v: unknown): string {
  return v === undefined || v === null ? "" : typeof v === "string" ? v : JSON.stringify(v);
}

/** The setting's range, for its input's hint. */
export function settingRange(prop: SchemaProperty): string {
  if (prop.minimum !== undefined && prop.maximum !== undefined) return `${prop.minimum}–${prop.maximum}`;
  if (prop.minimum !== undefined) return `≥ ${prop.minimum}`;
  if (prop.maximum !== undefined) return `≤ ${prop.maximum}`;
  return "";
}

/**
 * The settings the plugin's manifest declares, with the values of one
 * config: the global one's, or channelId's project's. A setting left unset
 * follows the value below it: the global value, then the manifest's
 * default. Empty, an input unsets its setting.
 */
export function PluginSettings({ colors, plugin, channelId, onChanged }: { colors: ColorPalette; plugin: PluginInfo; channelId?: string; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const keys = settingKeys(plugin.settings_schema);
  if (keys.length === 0) return null;
  const own = (channelId ? plugin.project_settings : plugin.global_settings) ?? {};

  const save = async (key: string, value: unknown) => {
    setBusy(true);
    setError(null);
    try {
      await setPluginSetting(plugin.id, key, value, channelId);
      onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-testid={`plugin-settings-${plugin.id}`} style={{ marginTop: 8 }}>
      <div style={{ marginBottom: 4, color: colors.textDim }}>Settings</div>
      {keys.map((key) => {
        const prop = plugin.settings_schema![key]!;
        const below = channelId ? (plugin.global_settings?.[key] ?? prop.default) : prop.default;
        return (
          <div key={key} style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 4 }}>
            <span style={{ flex: 1, minWidth: 0 }} title={prop.description}>
              {prop.title || key} <span style={{ color: colors.textDim, fontFamily: fonts.mono, fontSize: 11 }}>{key}</span>
            </span>
            <SettingInput colors={colors} testId={`plugin-setting-${plugin.id}-${key}`} prop={prop} value={own[key]} below={below} disabled={busy} onSave={(v) => save(key, v)} onError={setError} />
          </div>
        );
      })}
      {channelId && !!plugin.settings_problems?.length && (
        <div data-testid={`plugin-settings-problems-${plugin.id}`} style={{ color: colors.warning }}>
          Left out: {plugin.settings_problems.join("; ")}
        </div>
      )}
      {error && <div style={{ color: colors.error }}>{error}</div>}
    </div>
  );
}

const inputStyle = (colors: ColorPalette): React.CSSProperties => ({
  width: 160,
  boxSizing: "border-box",
  padding: "3px 6px",
  backgroundColor: colors.bg,
  border: `1px solid ${colors.border}`,
  borderRadius: 4,
  color: colors.text,
  fontSize: 12,
  fontFamily: "inherit",
});

/** One setting's input: a select for an enum or a boolean, a text box for
 *  the rest, saved as it loses focus or on Enter. below is the value an
 *  unset setting follows. */
function SettingInput({
  colors,
  testId,
  prop,
  value,
  below,
  disabled,
  onSave,
  onError,
}: {
  colors: ColorPalette;
  testId: string;
  prop: SchemaProperty;
  value: unknown;
  below: unknown;
  disabled: boolean;
  onSave: (v: unknown) => void;
  onError: (e: string) => void;
}) {
  const [text, setText] = useState(settingText(value));
  useEffect(() => setText(settingText(value)), [value]);
  const unset = `default${below === undefined ? "" : ` (${settingText(below)})`}`;

  if (prop.enum || prop.type === "boolean") {
    const options = prop.enum ?? [true, false];
    const at = options.indexOf(value);
    return (
      <select
        data-testid={testId}
        value={at < 0 ? "" : String(at)}
        disabled={disabled}
        onChange={(e) => onSave(e.target.value === "" ? undefined : options[Number(e.target.value)])}
        style={inputStyle(colors)}
      >
        <option value="">{unset}</option>
        {options.map((o, i) => (
          <option key={settingText(o)} value={String(i)}>
            {settingText(o)}
          </option>
        ))}
      </select>
    );
  }

  const commit = () => {
    if (text === settingText(value)) return;
    const got = parseSetting(prop, text);
    if ("error" in got) {
      onError(got.error);
      return;
    }
    onSave(got.value);
  };
  const range = settingRange(prop);
  return (
    <input
      data-testid={testId}
      value={text}
      placeholder={range ? `${unset} · ${range}` : unset}
      disabled={disabled}
      inputMode={prop.type === "integer" || prop.type === "number" ? "decimal" : undefined}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit();
      }}
      style={inputStyle(colors)}
    />
  );
}
