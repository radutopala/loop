import React, { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { ConfigResponse, ConfigSchema, SchemaProperty } from "../../api/configApi";
import { useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { builtinThemes, fonts } from "../../theme";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Leaf value that a single config field can hold. */
type FieldValue = string | number | boolean | string[] | Record<string, string>;

/** Recursive config data — objects can nest arbitrarily. */
type ConfigData = { [key: string]: ConfigData | FieldValue | ConfigData[] | undefined };

/** Value accepted by the object-array / object-map item sub-fields. */
type ItemFieldValue = string | number | boolean | string[] | Record<string, any>[] | Record<string, any>;

interface FieldDef {
  key: string;
  label: string;
  description?: string;
  type: "text" | "password" | "number" | "toggle" | "dropdown" | "array" | "multiselect" | "keyvalue" | "objectarray" | "objectmap";
  options?: string[];
  placeholder?: string;
  section: string;
  step?: number;
  itemProperties?: Record<string, SchemaProperty>; // for objectarray and objectmap (additionalProperties.properties)
  widget?: string;
  defaultValue?: FieldValue;
  autoSave?: boolean;
}

export interface ConfigFormProps {
  schema: ConfigSchema | null;
  config: ConfigResponse | null;
  onSave: (content: string) => Promise<string | null>;
  isGlobal: boolean;
  title: string;
  colors: ColorPalette;
  onDirtyChange?: (dirty: boolean) => void;
  visibleSection?: string;
  jsonOnly?: boolean;
}

/** Extract ordered section names from a schema. */
export function getSections(schema: ConfigSchema | null, isGlobal: boolean): string[] {
  if (!schema) return [];
  const fields = schemaToFields(schema, isGlobal);
  const seen = new Set<string>();
  const result: string[] = [];
  for (const f of fields) {
    if (!seen.has(f.section)) {
      seen.add(f.section);
      result.push(f.section);
    }
  }
  return result;
}

// ---------------------------------------------------------------------------
// Schema-driven field generation
// ---------------------------------------------------------------------------

function schemaToFields(schema: ConfigSchema, isGlobal: boolean): FieldDef[] {
  const fields: FieldDef[] = [];

  function processProperties(props: Record<string, SchemaProperty>, prefix: string, parentSection?: string, parentTitle?: string, parentAutoSave?: boolean) {
    const entries = Object.entries(props).sort(([, a], [, b]) => (a["x-order"] ?? 999) - (b["x-order"] ?? 999));

    for (const [key, prop] of entries) {
      const fullKey = prefix ? `${prefix}.${key}` : key;
      const section = prop["x-section"] ?? parentSection ?? "General";
      const autoSave = prop["x-auto-save"] ?? parentAutoSave;

      // Skip global-only fields in project config
      if (!isGlobal && prop["x-global-only"]) continue;

      // Recurse into nested objects that have properties (but not additionalProperties — those are key-value maps)
      if (prop.type === "object" && prop.properties && !prop.additionalProperties) {
        processProperties(prop.properties, fullKey, section, prop.title, autoSave);
        continue;
      }

      const label = parentTitle ? `${parentTitle} ${prop.title ?? key}` : (prop.title ?? key);
      fields.push({
        key: fullKey,
        label,
        description: prop.description,
        type: inferFieldType(prop),
        options: (prop.enum ?? prop.items?.enum)?.map(String),
        placeholder: prop["x-placeholder"],
        section,
        step: prop["x-step"],
        itemProperties: prop.items?.properties ?? prop.additionalProperties?.properties,
        widget: prop["x-widget"],
        defaultValue: prop.default,
        autoSave,
      });
    }
  }

  processProperties(schema.properties, "");
  return fields;
}

function inferFieldType(prop: SchemaProperty): FieldDef["type"] {
  if (prop["x-secret"]) return "password";
  if (prop.enum) return "dropdown";
  if (prop.type === "boolean") return "toggle";
  if (prop.type === "integer" || prop.type === "number") return "number";
  if (prop.type === "array" && prop.items?.enum) return "multiselect";
  if (prop.type === "array" && prop.items?.type === "object" && prop.items?.properties) return "objectarray";
  if (prop.type === "array") return "array";
  if (prop.type === "object" && prop.additionalProperties?.properties) return "objectmap";
  if (prop.type === "object" && prop.additionalProperties) return "keyvalue";
  return "text";
}

// ---------------------------------------------------------------------------
// SVG icon helpers (avoid repeating markup)
// ---------------------------------------------------------------------------

const XIcon = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="18" y1="6" x2="6" y2="18" />
    <line x1="6" y1="6" x2="18" y2="18" />
  </svg>
);

const PlusIcon = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="12" y1="5" x2="12" y2="19" />
    <line x1="5" y1="12" x2="19" y2="12" />
  </svg>
);

// ---------------------------------------------------------------------------
// Value helpers
// ---------------------------------------------------------------------------

function getNestedValue(obj: ConfigData, path: string): ConfigData | FieldValue | ConfigData[] | undefined {
  let cur: ConfigData | FieldValue | ConfigData[] | undefined = obj;
  for (const p of path.split(".")) {
    if (cur == null || typeof cur !== "object" || Array.isArray(cur)) return undefined;
    cur = cur[p];
  }
  return cur;
}

function setNestedValue(obj: ConfigData, path: string, value: ConfigData | FieldValue | ConfigData[] | undefined): ConfigData {
  const idx = path.indexOf(".");
  const clone: ConfigData = { ...obj };
  if (idx === -1) {
    clone[path] = value;
    return clone;
  }
  const key = path.slice(0, idx);
  const rest = path.slice(idx + 1);
  const child = clone[key];
  clone[key] = setNestedValue(typeof child === "object" && child !== null && !Array.isArray(child) ? { ...child } : {}, rest, value);
  return clone;
}

function cleanForSerialize(obj: ConfigData | FieldValue | ConfigData[] | undefined | null): ConfigData | FieldValue | ConfigData[] | undefined {
  if (obj === null || obj === undefined) return undefined;
  if (Array.isArray(obj)) {
    const f = obj.filter((v) => v != null && v !== "");
    return f.length ? (f as FieldValue | ConfigData[]) : undefined;
  }
  if (typeof obj === "object") {
    const r: ConfigData = {};
    let has = false;
    for (const [k, v] of Object.entries(obj)) {
      const c = cleanForSerialize(v as ConfigData | FieldValue | ConfigData[] | undefined);
      if (c !== undefined) {
        r[k] = c;
        has = true;
      }
    }
    return has ? r : undefined;
  }
  return obj === "" ? undefined : obj;
}

// ---------------------------------------------------------------------------
// Shared small components
// ---------------------------------------------------------------------------

function RemoveBtn({ onClick, colors }: { onClick: () => void; colors: ColorPalette }) {
  return (
    <button
      onClick={onClick}
      style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 2, lineHeight: 1, display: "flex", alignItems: "center", flexShrink: 0 }}
      onMouseEnter={(e) => {
        e.currentTarget.style.color = colors.error;
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.color = colors.textDim;
      }}
    >
      <XIcon />
    </button>
  );
}

function AddBtn({ onClick, colors }: { onClick: () => void; colors: ColorPalette }) {
  return (
    <button
      onClick={onClick}
      style={{
        background: "none",
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
        color: colors.textDim,
        cursor: "pointer",
        padding: "4px 8px",
        lineHeight: 1,
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.borderColor = colors.textDim;
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.borderColor = colors.border;
      }}
    >
      <PlusIcon />
    </button>
  );
}

function FieldLabel({ field, colors }: { field: FieldDef; colors: ColorPalette }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{ fontSize: 13, color: colors.text }}>{field.label}</div>
      {field.description && <div style={{ fontSize: 11, color: colors.textDim, marginTop: 1 }}>{field.description}</div>}
    </div>
  );
}

const itemTagStyle = (colors: ColorPalette): React.CSSProperties => ({
  fontSize: 12,
  fontFamily: fonts.mono,
  backgroundColor: colors.surface,
  borderRadius: 4,
  padding: "3px 8px",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
});

const rowBorder = (colors: ColorPalette): React.CSSProperties => ({ borderBottom: `1px solid ${colors.border}` });

// ---------------------------------------------------------------------------
// ConfigForm
// ---------------------------------------------------------------------------

export interface ConfigFormHandle {
  save: () => Promise<void>;
  cancel: () => void;
}

export const ConfigForm = forwardRef<ConfigFormHandle, ConfigFormProps>(function ConfigForm(
  { schema, config, onSave, isGlobal, title, colors, onDirtyChange, visibleSection, jsonOnly }: ConfigFormProps,
  ref,
) {
  const [viewMode, setViewMode] = useState<"form" | "json">(jsonOnly ? "json" : "form");
  const [formData, setFormData] = useState<ConfigData>({});
  const [jsonDraft, setJsonDraft] = useState("");
  const [, setDirtyRaw] = useState(false);
  const [, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const setDirty = (v: boolean) => {
    setDirtyRaw(v);
    onDirtyChange?.(v);
  };

  useEffect(() => {
    if (!config) {
      setFormData({});
      setJsonDraft("{\n  \n}\n");
      setDirty(false);
      return;
    }
    const parsed = (config.content ?? {}) as ConfigData;
    setFormData(parsed);
    setJsonDraft(config.raw ?? JSON.stringify(parsed, null, 2) + "\n");
    setDirty(false);
    setError(null);
  }, [config]);

  const switchView = useCallback(
    (mode: "form" | "json") => {
      if (mode === viewMode) return;
      if (viewMode === "form") {
        setJsonDraft(JSON.stringify(cleanForSerialize(formData) ?? {}, null, 2) + "\n");
      } else {
        try {
          setFormData(JSON.parse(jsonDraft) as ConfigData);
          setError(null);
        } catch (e: unknown) {
          setError("Invalid JSON: " + (e instanceof Error ? e.message : "parse error"));
          return;
        }
      }
      setViewMode(mode);
    },
    [viewMode, formData, jsonDraft],
  );

  const handleSave = useCallback(async () => {
    setSaving(true);
    setError(null);
    const content = viewMode === "json" ? jsonDraft : JSON.stringify(cleanForSerialize(formData) ?? {}, null, 2) + "\n";
    const err = await onSave(content);
    setSaving(false);
    if (err) setError(err);
    else setDirty(false);
  }, [viewMode, formData, jsonDraft, onSave]);

  // Auto-save: enabled when all visible fields have the x-auto-save flag set in the schema.
  const allFields = schema ? schemaToFields(schema, isGlobal) : [];
  const visibleAutoSaveFields = visibleSection ? allFields.filter((f) => f.section === visibleSection) : allFields;
  const autoSave = visibleAutoSaveFields.length > 0 && visibleAutoSaveFields.every((f) => f.autoSave);

  const handleSaveRef = useRef(handleSave);
  handleSaveRef.current = handleSave;
  const autoSaveTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(autoSaveTimerRef.current), []);

  const handleFormChange = useCallback(
    (key: string, value: ConfigData | FieldValue | ConfigData[] | undefined) => {
      setFormData((prev) => setNestedValue(prev, key, value));
      if (autoSave) {
        clearTimeout(autoSaveTimerRef.current);
        autoSaveTimerRef.current = setTimeout(() => handleSaveRef.current(), 100);
      } else {
        setDirty(true);
      }
    },
    [autoSave],
  );

  const handleCancel = useCallback(() => {
    if (!config) {
      setFormData({});
      setJsonDraft("{\n  \n}\n");
    } else {
      const parsed = (config.content ?? {}) as ConfigData;
      setFormData(parsed);
      setJsonDraft(config.raw ?? JSON.stringify(parsed, null, 2) + "\n");
    }
    setDirty(false);
    setError(null);
  }, [config]);

  useImperativeHandle(ref, () => ({ save: handleSave, cancel: handleCancel }), [handleSave, handleCancel]);

  const fields = schema ? schemaToFields(schema, isGlobal) : [];
  const sections: { name: string; fields: FieldDef[] }[] = [];
  const map = new Map<string, FieldDef[]>();
  for (const f of fields) {
    if (visibleSection && f.section !== visibleSection) continue;
    if (!map.has(f.section)) {
      const a: FieldDef[] = [];
      map.set(f.section, a);
      sections.push({ name: f.section, fields: a });
    }
    map.get(f.section)!.push(f);
  }

  const inputStyle: React.CSSProperties = {
    backgroundColor: colors.bg,
    border: `1px solid ${colors.border}`,
    borderRadius: 6,
    padding: "5px 8px",
    fontSize: 12,
    fontFamily: fonts.mono,
    color: colors.text,
    outline: "none",
    boxSizing: "border-box",
  };

  if (!schema) {
    return (
      <div style={{ marginTop: 20 }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>{title}</div>
        <div style={{ fontSize: 12, color: colors.textDim, marginTop: 8 }}>Loading schema...</div>
      </div>
    );
  }

  return (
    <div
      style={{
        ...(!(visibleSection || jsonOnly) && { marginTop: 20 }),
        ...(jsonOnly && { display: "flex", flexDirection: "column" as const, flex: 1, minHeight: 0 }),
      }}
    >
      {/* Header: title + pill toggle (hidden when filtered to a single section or jsonOnly) */}
      {!visibleSection && !jsonOnly && (
        <>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
            <div style={{ fontSize: 11, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>{title}</div>
            <div style={{ display: "flex", border: `1px solid ${colors.border}`, borderRadius: 6, overflow: "hidden" }}>
              {(["form", "json"] as const).map((m) => (
                <button
                  key={m}
                  onClick={() => switchView(m)}
                  style={{
                    padding: "3px 10px",
                    fontSize: 11,
                    fontWeight: 500,
                    fontFamily: "inherit",
                    border: "none",
                    cursor: "pointer",
                    backgroundColor: viewMode === m ? colors.pillActiveBg : "transparent",
                    color: viewMode === m ? colors.pillActiveText : colors.textDim,
                    transition: "background-color 0.15s, color 0.15s",
                  }}
                >
                  {m === "form" ? "Form" : "JSON"}
                </button>
              ))}
            </div>
          </div>

          <div style={{ fontSize: 11, fontFamily: fonts.mono, color: colors.textDim, marginBottom: 8, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{config?.path}</div>
        </>
      )}

      {jsonOnly && config?.path && (
        <div style={{ fontSize: 11, fontFamily: fonts.mono, color: colors.textDim, marginBottom: 8, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{config.path}</div>
      )}

      {viewMode === "json" ? (
        <JSONView
          colors={colors}
          draft={jsonDraft}
          onChange={(v) => {
            setJsonDraft(v);
            setDirty(true);
          }}
          textareaRef={textareaRef}
          onSave={handleSave}
          error={error}
          fillHeight={jsonOnly}
        />
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          {sections.map((sec) => (
            <div key={sec.name}>
              {!visibleSection && <div style={{ fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.8, marginBottom: 6 }}>{sec.name}</div>}
              <div style={{ backgroundColor: colors.bg, borderRadius: 8, padding: "4px 0" }}>
                {sec.fields.map((f) => (
                  <FieldRenderer key={f.key} field={f} value={getNestedValue(formData, f.key)} onChange={(v) => handleFormChange(f.key, v)} colors={colors} inputStyle={inputStyle} />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {error && <div style={{ fontSize: 11, color: colors.error, marginTop: 6 }}>{error}</div>}
    </div>
  );
});

// ---------------------------------------------------------------------------
// JSON View
// ---------------------------------------------------------------------------

function JSONView({
  colors,
  draft,
  onChange,
  textareaRef,
  onSave,
  error,
  fillHeight,
}: {
  colors: ColorPalette;
  draft: string;
  onChange: (v: string) => void;
  textareaRef: React.RefObject<HTMLTextAreaElement | null>;
  onSave: () => void;
  error: string | null;
  fillHeight?: boolean;
}) {
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "s") {
      e.preventDefault();
      onSave();
    }
    if (e.key === "Tab") {
      e.preventDefault();
      const ta = textareaRef.current;
      if (!ta) return;
      const s = ta.selectionStart,
        end = ta.selectionEnd;
      onChange(draft.substring(0, s) + "  " + draft.substring(end));
      setTimeout(() => {
        ta.selectionStart = ta.selectionEnd = s + 2;
      }, 0);
    }
  };
  return (
    <div style={fillHeight ? { display: "flex", flexDirection: "column", flex: 1, minHeight: 0 } : undefined}>
      <textarea
        ref={textareaRef}
        value={draft}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={handleKeyDown}
        spellCheck={false}
        style={{
          width: "100%",
          ...(fillHeight ? { flex: 1, minHeight: 0, resize: "none" } : { minHeight: 200, maxHeight: 500, resize: "vertical" }),
          backgroundColor: colors.bg,
          border: `1px solid ${error ? colors.error : colors.border}`,
          borderRadius: 8,
          padding: "10px 12px",
          fontSize: 12,
          fontFamily: fonts.mono,
          color: colors.text,
          lineHeight: 1.5,
          outline: "none",
          boxSizing: "border-box",
        }}
      />
      <div style={{ fontSize: 10, color: colors.textDim, marginTop: 4, textAlign: "right", flexShrink: 0 }}>{navigator.platform.includes("Mac") ? "\u2318S" : "Ctrl+S"} to save</div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Field Renderer (dispatcher)
// ---------------------------------------------------------------------------

function FieldRenderer({
  field,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  field: FieldDef;
  value: ConfigData | FieldValue | ConfigData[] | undefined;
  onChange: (v: ConfigData | FieldValue | ConfigData[] | undefined) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  // Narrow onChange callbacks for sub-components — safe because the field.type discriminator
  // guarantees the runtime value will match the narrower type at each call site.
  const onStr = onChange as (v: string) => void;
  const onNum = onChange as (v: number | undefined) => void;
  const onBool = onChange as (v: boolean) => void;
  const onStrArr = onChange as (v: string[]) => void;
  const onObjArr = onChange as (v: Record<string, ItemFieldValue>[]) => void;
  const onKV = onChange as (v: Record<string, string>) => void;
  const onObjMap = onChange as (v: Record<string, Record<string, ItemFieldValue>>) => void;
  const onStepper = onChange as (v: number) => void;

  // Custom widget overrides
  if (field.widget === "theme-picker") return <ThemePickerFieldRow field={field} value={(value ?? "") as string} onChange={onStr} colors={colors} />;
  if (field.widget === "stepper") return <StepperFieldRow field={field} value={value as number | undefined} onChange={onStepper} colors={colors} />;

  switch (field.type) {
    case "text":
      return <TextFieldRow field={field} value={(value ?? "") as string} onChange={onStr} colors={colors} inputStyle={inputStyle} />;
    case "password":
      return <PasswordFieldRow field={field} value={(value ?? "") as string} onChange={onStr} colors={colors} inputStyle={inputStyle} />;
    case "number":
      return <NumberFieldRow field={field} value={value as number | undefined} onChange={onNum} colors={colors} inputStyle={inputStyle} />;
    case "toggle":
      return <ToggleFieldRow field={field} value={!!(value ?? field.defaultValue)} onChange={onBool} colors={colors} />;
    case "dropdown":
      return <DropdownFieldRow field={field} value={(value ?? "") as string} onChange={onStr} colors={colors} inputStyle={inputStyle} />;
    case "array":
      return <ArrayFieldRow field={field} value={(value ?? []) as string[]} onChange={onStrArr} colors={colors} inputStyle={inputStyle} />;
    case "multiselect":
      return <MultiSelectFieldRow field={field} value={(value ?? []) as string[]} onChange={onStrArr} colors={colors} />;
    case "objectarray":
      return <ObjectArrayFieldRow field={field} value={(value ?? []) as Record<string, ItemFieldValue>[]} onChange={onObjArr} colors={colors} inputStyle={inputStyle} />;
    case "keyvalue":
      return <KeyValueFieldRow field={field} value={(value ?? {}) as Record<string, string>} onChange={onKV} colors={colors} inputStyle={inputStyle} />;
    case "objectmap":
      return <ObjectMapFieldRow field={field} value={(value ?? {}) as Record<string, Record<string, ItemFieldValue>>} onChange={onObjMap} colors={colors} inputStyle={inputStyle} />;
    default:
      return null;
  }
}

// ---------------------------------------------------------------------------
// Text
// ---------------------------------------------------------------------------

function TextFieldRow({ field, value, onChange, colors, inputStyle }: { field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette; inputStyle: React.CSSProperties }) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <input type="text" value={value} onChange={(e) => onChange(e.target.value)} placeholder={field.placeholder} style={{ ...inputStyle, width: 180, flexShrink: 0 }} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Password
// ---------------------------------------------------------------------------

function PasswordFieldRow({ field, value, onChange, colors, inputStyle }: { field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette; inputStyle: React.CSSProperties }) {
  const [show, setShow] = useState(false);
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ display: "flex", alignItems: "center", gap: 4, flexShrink: 0 }}>
        <input type={show ? "text" : "password"} value={value} onChange={(e) => onChange(e.target.value)} placeholder={field.placeholder} style={{ ...inputStyle, width: 160 }} />
        <button
          onClick={() => setShow(!show)}
          title={show ? "Hide" : "Show"}
          style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 4, lineHeight: 1, display: "flex", alignItems: "center" }}
        >
          {show ? (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
              <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
              <line x1="1" y1="1" x2="23" y2="23" />
            </svg>
          ) : (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
              <circle cx="12" cy="12" r="3" />
            </svg>
          )}
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Number
// ---------------------------------------------------------------------------

function NumberFieldRow({
  field,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  field: FieldDef;
  value: number | undefined;
  onChange: (v: number | undefined) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <input
        type="number"
        value={value ?? ""}
        step={field.step}
        placeholder={field.placeholder}
        onChange={(e) => {
          const r = e.target.value;
          if (r === "") {
            onChange(undefined);
            return;
          }
          const n = Number(r);
          onChange(isNaN(n) ? undefined : n);
        }}
        style={{ ...inputStyle, width: 100, flexShrink: 0 }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Toggle (matches Settings.tsx ToggleRow switch)
// ---------------------------------------------------------------------------

function ToggleFieldRow({ field, value, onChange, colors }: { field: FieldDef; value: boolean; onChange: (v: boolean) => void; colors: ColorPalette }) {
  return (
    <div onClick={() => onChange(!value)} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", cursor: "pointer", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ width: 36, height: 20, borderRadius: 10, backgroundColor: value ? colors.active : colors.border, position: "relative", flexShrink: 0, transition: "background-color 0.2s" }}>
        <div style={{ width: 16, height: 16, borderRadius: "50%", backgroundColor: colors.white, position: "absolute", top: 2, left: value ? 18 : 2, transition: "left 0.2s" }} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Dropdown
// ---------------------------------------------------------------------------

function DropdownFieldRow({ field, value, onChange, colors, inputStyle }: { field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette; inputStyle: React.CSSProperties }) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <select value={value} onChange={(e) => onChange(e.target.value)} style={{ ...inputStyle, width: 180, flexShrink: 0, cursor: "pointer" }}>
        {(field.options ?? []).map((o) => (
          <option key={o} value={o}>
            {o || "(default)"}
          </option>
        ))}
      </select>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Array
// ---------------------------------------------------------------------------

function MultiSelectFieldRow({ field, value, onChange, colors }: { field: FieldDef; value: string[]; onChange: (v: string[]) => void; colors: ColorPalette }) {
  const options = field.options ?? [];
  const selected = new Set(Array.isArray(value) ? value : []);
  const toggle = (opt: string) => {
    const next = new Set(selected);
    if (next.has(opt)) next.delete(opt);
    else next.add(opt);
    onChange([...next]);
  };
  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors), display: "flex", alignItems: "center", justifyContent: "space-between" }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ display: "flex", gap: 6 }}>
        {options.map((opt) => (
          <button
            key={opt}
            onClick={() => toggle(opt)}
            style={{
              padding: "3px 10px",
              fontSize: 12,
              borderRadius: 4,
              cursor: "pointer",
              fontFamily: "inherit",
              backgroundColor: selected.has(opt) ? colors.active : "transparent",
              color: selected.has(opt) ? colors.white : colors.textDim,
              border: `1px solid ${selected.has(opt) ? colors.active : colors.border}`,
            }}
          >
            {opt}
          </button>
        ))}
      </div>
    </div>
  );
}

function ObjectArrayFieldRow({
  field,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  field: FieldDef;
  value: Record<string, ItemFieldValue>[];
  onChange: (v: Record<string, ItemFieldValue>[]) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  const items: Record<string, ItemFieldValue>[] = Array.isArray(value) ? value : [];
  const props = field.itemProperties ?? {};
  const propKeys = Object.entries(props).sort(([, a], [, b]) => (a["x-order"] ?? 999) - (b["x-order"] ?? 999));

  const addItem = () => {
    const empty: Record<string, ItemFieldValue> = {};
    for (const [k, p] of propKeys) {
      if (p.type === "string") empty[k] = "";
      else if (p.type === "integer" || p.type === "number") empty[k] = 0;
      else if (p.type === "boolean") empty[k] = false;
      else if (p.type === "array") empty[k] = [];
      else if (p.type === "object") empty[k] = {};
    }
    onChange([...items, empty]);
  };

  const updateItem = (idx: number, key: string, val: ItemFieldValue) => {
    const next = items.map((item, i) => (i === idx ? { ...item, [key]: val } : item));
    onChange(next);
  };

  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      {items.map((item, idx) => (
        <React.Fragment key={idx}>
          {idx > 0 && <div style={{ height: 1, background: colors.border, margin: "10px 0 2px" }} />}
          <div style={{ backgroundColor: colors.bg, borderRadius: 8, padding: "8px 10px", marginTop: 8, position: "relative" }}>
            <div style={{ position: "absolute", top: 4, right: 4 }}>
              <RemoveBtn onClick={() => onChange(items.filter((_, i) => i !== idx))} colors={colors} />
            </div>
            {propKeys.map(([k, p]) => {
              if (p.enum) {
                return (
                  <div key={k} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "3px 0" }}>
                    <span style={{ fontSize: 12, color: colors.textDim, minWidth: 80 }}>{p.title ?? k}</span>
                    <select value={(item[k] ?? "") as string} onChange={(e) => updateItem(idx, k, e.target.value)} style={{ ...inputStyle, flex: 1, maxWidth: 200 }}>
                      <option value="">—</option>
                      {p.enum.map((v: string | number | boolean) => (
                        <option key={String(v)} value={String(v)}>
                          {String(v)}
                        </option>
                      ))}
                    </select>
                  </div>
                );
              }
              if (p.type === "boolean") {
                return (
                  <div key={k} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "3px 0" }}>
                    <span style={{ fontSize: 12, color: colors.textDim, minWidth: 80 }}>{p.title ?? k}</span>
                    <input type="checkbox" checked={!!item[k]} onChange={(e) => updateItem(idx, k, e.target.checked)} />
                  </div>
                );
              }
              if (p["x-widget"] === "textarea") {
                return (
                  <div key={k} style={{ padding: "3px 0" }}>
                    <span style={{ fontSize: 12, color: colors.textDim }}>{p.title ?? k}</span>
                    <textarea
                      value={(item[k] ?? "") as string}
                      onChange={(e) => updateItem(idx, k, e.target.value)}
                      placeholder={p["x-placeholder"] ?? ""}
                      rows={3}
                      style={{ ...inputStyle, width: "100%", marginTop: 4, resize: "vertical", fontFamily: fonts.mono, fontSize: 12, lineHeight: 1.5 }}
                    />
                  </div>
                );
              }
              if (p.type === "array" && p.items?.type === "string") {
                return <ObjectMapArrayProp key={k} label={p.title ?? k} value={(item[k] ?? []) as string[]} onChange={(v) => updateItem(idx, k, v)} colors={colors} inputStyle={inputStyle} />;
              }
              if (p.type === "array" && p.items?.type === "object" && p.items?.properties) {
                const nestedField: FieldDef = { key: k, label: p.title ?? k, type: "objectarray", section: "", itemProperties: p.items.properties };
                return (
                  <div key={k} style={{ padding: "3px 0" }}>
                    <ObjectArrayFieldRow
                      field={nestedField}
                      value={(item[k] ?? []) as Record<string, ItemFieldValue>[]}
                      onChange={(v) => updateItem(idx, k, v)}
                      colors={colors}
                      inputStyle={inputStyle}
                    />
                  </div>
                );
              }
              if (p.type === "object" && p.additionalProperties?.properties) {
                const nestedField: FieldDef = { key: k, label: p.title ?? k, type: "objectmap", section: "", itemProperties: p.additionalProperties.properties };
                return (
                  <div key={k} style={{ padding: "3px 0" }}>
                    <ObjectMapFieldRow
                      field={nestedField}
                      value={(item[k] ?? {}) as Record<string, Record<string, ItemFieldValue>>}
                      onChange={(v) => updateItem(idx, k, v)}
                      colors={colors}
                      inputStyle={inputStyle}
                    />
                  </div>
                );
              }
              if (p.type === "object" && p.properties) {
                const nestedProps = Object.entries(p.properties).sort(([, a], [, b]) => (a["x-order"] ?? 999) - (b["x-order"] ?? 999));
                const obj = (item[k] ?? {}) as Record<string, any>;
                return (
                  <div key={k} style={{ padding: "3px 0" }}>
                    <span style={{ fontSize: 12, color: colors.textDim }}>{p.title ?? k}</span>
                    <div style={{ paddingLeft: 12, marginTop: 4 }}>
                      {nestedProps.map(([nk, np]) => (
                        <div key={nk} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "3px 0" }}>
                          <span style={{ fontSize: 12, color: colors.textDim, minWidth: 80 }}>{np.title ?? nk}</span>
                          <input
                            type={np.type === "integer" ? "number" : "text"}
                            value={(obj[nk] ?? "") as string | number}
                            onChange={(e) => updateItem(idx, k, { ...obj, [nk]: np.type === "integer" ? Number(e.target.value) : e.target.value })}
                            placeholder={np["x-placeholder"] ?? ""}
                            style={{ ...inputStyle, flex: 1 }}
                          />
                        </div>
                      ))}
                    </div>
                  </div>
                );
              }
              return (
                <div key={k} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "3px 0" }}>
                  <span style={{ fontSize: 12, color: colors.textDim, minWidth: 80 }}>{p.title ?? k}</span>
                  <input
                    type={p.type === "integer" ? "number" : "text"}
                    value={(item[k] ?? "") as string | number}
                    onChange={(e) => updateItem(idx, k, p.type === "integer" ? Number(e.target.value) : e.target.value)}
                    placeholder={p["x-placeholder"] ?? ""}
                    style={{ ...inputStyle, flex: 1 }}
                  />
                </div>
              );
            })}
          </div>
        </React.Fragment>
      ))}
      <div style={{ marginTop: 8 }}>
        <AddBtn onClick={addItem} colors={colors} />
      </div>
    </div>
  );
}

function ArrayFieldRow({ field, value, onChange, colors, inputStyle }: { field: FieldDef; value: string[]; onChange: (v: string[]) => void; colors: ColorPalette; inputStyle: React.CSSProperties }) {
  const [draft, setDraft] = useState("");
  const items: string[] = Array.isArray(value) ? value : [];
  const add = () => {
    const t = draft.trim();
    if (!t) return;
    onChange([...items, t]);
    setDraft("");
  };

  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      {items.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 6 }}>
          {items.map((item, i) => (
            <div key={i} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <span style={{ ...itemTagStyle(colors), flex: 1, color: colors.text }}>{typeof item === "object" ? JSON.stringify(item) : String(item)}</span>
              <RemoveBtn onClick={() => onChange(items.filter((_, j) => j !== i))} colors={colors} />
            </div>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6 }}>
        <input
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder={field.placeholder ?? "Add item..."}
          style={{ ...inputStyle, flex: 1 }}
        />
        <AddBtn onClick={add} colors={colors} />
        {(field.key === "extra_dirs" || field.key === "memory.paths") && window.loopAPI?.showOpenDirectoryDialog && (
          <button
            onClick={async () => {
              const dir = await window.loopAPI?.showOpenDirectoryDialog?.();
              if (dir) onChange([...items, dir]);
            }}
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              borderRadius: 6,
              color: colors.textDim,
              cursor: "pointer",
              padding: "3px 8px",
              fontSize: 11,
              fontFamily: "inherit",
              whiteSpace: "nowrap",
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.borderColor = colors.textDim;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.borderColor = colors.border;
            }}
          >
            Browse...
          </button>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Key-Value
// ---------------------------------------------------------------------------

function KeyValueFieldRow({
  field,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  field: FieldDef;
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  const [dk, setDk] = useState("");
  const [dv, setDv] = useState("");
  const entries = Object.entries(value ?? {});
  const add = () => {
    const k = dk.trim();
    if (!k) return;
    onChange({ ...value, [k]: dv });
    setDk("");
    setDv("");
  };

  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      {entries.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 6 }}>
          {entries.map(([k, v]) => (
            <div key={k} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <span style={{ ...itemTagStyle(colors), color: colors.textMuted }}>{k}</span>
              <span style={{ fontSize: 12, color: colors.textDim }}>=</span>
              <span style={{ ...itemTagStyle(colors), flex: 1, color: colors.text }}>{v}</span>
              <RemoveBtn
                onClick={() => {
                  const n = { ...value };
                  delete n[k];
                  onChange(n);
                }}
                colors={colors}
              />
            </div>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6 }}>
        <input
          type="text"
          value={dk}
          onChange={(e) => setDk(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder="KEY"
          style={{ ...inputStyle, width: 100 }}
        />
        <span style={{ fontSize: 12, color: colors.textDim }}>=</span>
        <input
          type="text"
          value={dv}
          onChange={(e) => setDv(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder="value"
          style={{ ...inputStyle, flex: 1 }}
        />
        <AddBtn onClick={add} colors={colors} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Object Map (additionalProperties with properties — e.g. MCP servers)
// ---------------------------------------------------------------------------

function ObjectMapFieldRow({
  field,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  field: FieldDef;
  value: Record<string, Record<string, ItemFieldValue>>;
  onChange: (v: Record<string, Record<string, ItemFieldValue>>) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  const [newKey, setNewKey] = useState("");
  const entries = Object.entries(value ?? {});
  const props = field.itemProperties ?? {};
  const propKeys = Object.entries(props).sort(([, a], [, b]) => (a["x-order"] ?? 999) - (b["x-order"] ?? 999));

  const addEntry = () => {
    const k = newKey.trim();
    if (!k || value[k] !== undefined) return;
    const empty: Record<string, ItemFieldValue> = {};
    for (const [pk, p] of propKeys) {
      if (p.type === "string") empty[pk] = "";
      else if (p.type === "array") empty[pk] = [];
      else if (p.type === "object") empty[pk] = {};
    }
    onChange({ ...value, [k]: empty });
    setNewKey("");
  };

  const updateEntry = (entryKey: string, propKey: string, val: ItemFieldValue) => {
    onChange({ ...value, [entryKey]: { ...value[entryKey], [propKey]: val } });
  };

  const removeEntry = (entryKey: string) => {
    const next = { ...value };
    delete next[entryKey];
    onChange(next);
  };

  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      {entries.map(([entryKey, entryVal]) => (
        <div key={entryKey} style={{ backgroundColor: colors.bg, borderRadius: 8, padding: "8px 10px", marginTop: 8, position: "relative" }}>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 6 }}>
            <span style={{ fontSize: 12, fontWeight: 600, color: colors.text, fontFamily: fonts.mono }}>{entryKey}</span>
            <RemoveBtn onClick={() => removeEntry(entryKey)} colors={colors} />
          </div>
          {propKeys.map(([pk, p]) => {
            const v = entryVal?.[pk];
            if (p.type === "array" && p.items?.type === "string") {
              return <ObjectMapArrayProp key={pk} label={p.title ?? pk} value={(v ?? []) as string[]} onChange={(nv) => updateEntry(entryKey, pk, nv)} colors={colors} inputStyle={inputStyle} />;
            }
            if (p.type === "object" && p.additionalProperties?.type === "string") {
              return (
                <ObjectMapKVProp key={pk} label={p.title ?? pk} value={(v ?? {}) as Record<string, string>} onChange={(nv) => updateEntry(entryKey, pk, nv)} colors={colors} inputStyle={inputStyle} />
              );
            }
            return (
              <div key={pk} style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "3px 0" }}>
                <span style={{ fontSize: 12, color: colors.textDim, minWidth: 80 }}>{p.title ?? pk}</span>
                <input type="text" value={(v ?? "") as string} onChange={(e) => updateEntry(entryKey, pk, e.target.value)} placeholder={p["x-placeholder"] ?? ""} style={{ ...inputStyle, flex: 1 }} />
              </div>
            );
          })}
        </div>
      ))}
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 8 }}>
        <input
          type="text"
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              addEntry();
            }
          }}
          placeholder={`Add ${field.label.toLowerCase()}...`}
          style={{ ...inputStyle, flex: 1 }}
        />
        <AddBtn onClick={addEntry} colors={colors} />
      </div>
    </div>
  );
}

/** Inline array editor for an object-map property (e.g. args: string[]) */
function ObjectMapArrayProp({
  label,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  label: string;
  value: string[];
  onChange: (v: string[]) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const t = draft.trim();
    if (!t) return;
    onChange([...value, t]);
    setDraft("");
  };
  return (
    <div style={{ padding: "3px 0" }}>
      <span style={{ fontSize: 12, color: colors.textDim }}>{label}</span>
      {value.length > 0 && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 4, marginTop: 4 }}>
          {value.map((item, i) => (
            <span key={i} style={{ ...itemTagStyle(colors), display: "inline-flex", alignItems: "center", gap: 4, color: colors.text }}>
              {item}
              <span style={{ cursor: "pointer", color: colors.textDim, lineHeight: 1 }} onClick={() => onChange(value.filter((_, j) => j !== i))}>
                <XIcon />
              </span>
            </span>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 4 }}>
        <input
          type="text"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder="Add..."
          style={{ ...inputStyle, flex: 1 }}
        />
        <AddBtn onClick={add} colors={colors} />
      </div>
    </div>
  );
}

/** Inline key-value editor for an object-map property (e.g. env: Record<string, string>) */
function ObjectMapKVProp({
  label,
  value,
  onChange,
  colors,
  inputStyle,
}: {
  label: string;
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  colors: ColorPalette;
  inputStyle: React.CSSProperties;
}) {
  const [dk, setDk] = useState("");
  const [dv, setDv] = useState("");
  const entries = Object.entries(value ?? {});
  const add = () => {
    const k = dk.trim();
    if (!k) return;
    onChange({ ...value, [k]: dv });
    setDk("");
    setDv("");
  };
  return (
    <div style={{ padding: "3px 0" }}>
      <span style={{ fontSize: 12, color: colors.textDim }}>{label}</span>
      {entries.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 4 }}>
          {entries.map(([k, v]) => (
            <div key={k} style={{ display: "flex", alignItems: "center", gap: 4 }}>
              <span style={{ ...itemTagStyle(colors), color: colors.textMuted }}>{k}</span>
              <span style={{ fontSize: 12, color: colors.textDim }}>=</span>
              <span style={{ ...itemTagStyle(colors), flex: 1, color: colors.text }}>{v}</span>
              <span
                style={{ cursor: "pointer", color: colors.textDim, lineHeight: 1 }}
                onClick={() => {
                  const n = { ...value };
                  delete n[k];
                  onChange(n);
                }}
              >
                <XIcon />
              </span>
            </div>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 4, marginTop: 4 }}>
        <input
          type="text"
          value={dk}
          onChange={(e) => setDk(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder="KEY"
          style={{ ...inputStyle, width: 80 }}
        />
        <span style={{ fontSize: 12, color: colors.textDim }}>=</span>
        <input
          type="text"
          value={dv}
          onChange={(e) => setDv(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder="value"
          style={{ ...inputStyle, flex: 1 }}
        />
        <AddBtn onClick={add} colors={colors} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Theme Picker (x-widget: "theme-picker")
// ---------------------------------------------------------------------------

function ThemePickerFieldRow({ field, value, onChange, colors }: { field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette }) {
  const { themeName, availableThemes } = useTheme();
  const current = value || themeName;
  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
        {availableThemes.map((t) => {
          const palette = builtinThemes[t] ?? colors;
          const isSelected = current === t;
          return (
            <button
              key={t}
              onClick={() => onChange(t)}
              style={{
                flex: 1,
                border: `2px solid ${isSelected ? colors.active : colors.border}`,
                borderRadius: 8,
                padding: 0,
                cursor: "pointer",
                background: "none",
                overflow: "hidden",
              }}
            >
              <div style={{ display: "flex", height: 40 }}>
                <div style={{ width: "30%", backgroundColor: palette.sidebarNav }} />
                <div style={{ flex: 1, backgroundColor: palette.bg, display: "flex", flexDirection: "column", justifyContent: "center", alignItems: "center", gap: 3, padding: 4 }}>
                  <div style={{ width: "60%", height: 3, borderRadius: 2, backgroundColor: palette.textMuted }} />
                  <div style={{ width: "40%", height: 3, borderRadius: 2, backgroundColor: palette.active }} />
                  <div style={{ width: "50%", height: 3, borderRadius: 2, backgroundColor: palette.textMuted }} />
                </div>
              </div>
              <div
                style={{
                  fontSize: 11,
                  color: colors.text,
                  padding: "4px 0",
                  backgroundColor: colors.surface,
                  borderTop: `1px solid ${isSelected ? colors.active : colors.border}`,
                }}
              >
                {t.charAt(0).toUpperCase() + t.slice(1)}
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Stepper (x-widget: "stepper")
// ---------------------------------------------------------------------------

function StepperFieldRow({ field, value, onChange, colors }: { field: FieldDef; value: number | undefined; onChange: (v: number) => void; colors: ColorPalette }) {
  const current = typeof value === "number" ? value : typeof field.defaultValue === "number" ? field.defaultValue : 13;
  const btnStyle: React.CSSProperties = {
    width: 24,
    height: 24,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    backgroundColor: colors.surface,
    color: colors.text,
    cursor: "pointer",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    fontFamily: "inherit",
    fontSize: 14,
    lineHeight: 1,
    padding: 0,
  };
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ display: "flex", alignItems: "center", gap: 6, flexShrink: 0 }}>
        <button onClick={() => onChange(Math.max(8, current - 1))} style={btnStyle}>
          {"\u2212"}
        </button>
        <span style={{ fontSize: 12, color: colors.text, fontFamily: fonts.mono, minWidth: 32, textAlign: "center" }}>{current}px</span>
        <button onClick={() => onChange(Math.min(30, current + 1))} style={btnStyle}>
          +
        </button>
      </div>
    </div>
  );
}
