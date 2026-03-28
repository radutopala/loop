import { useCallback, useEffect, useRef, useState } from "react";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import type { ConfigSchema, SchemaProperty, ConfigResponse } from "../api/configApi";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface FieldDef {
  key: string;
  label: string;
  description?: string;
  type: "text" | "password" | "number" | "toggle" | "dropdown" | "array" | "multiselect" | "keyvalue";
  options?: string[];
  placeholder?: string;
  section: string;
  step?: number;
}

export interface ConfigFormProps {
  schema: ConfigSchema | null;
  config: ConfigResponse | null;
  onSave: (content: string) => Promise<string | null>;
  isGlobal: boolean;
  title: string;
  colors: ColorPalette;
  onDirtyChange?: (dirty: boolean) => void;
}

// ---------------------------------------------------------------------------
// Schema-driven field generation
// ---------------------------------------------------------------------------

function schemaToFields(schema: ConfigSchema, isGlobal: boolean): FieldDef[] {
  const fields: FieldDef[] = [];

  function processProperties(props: Record<string, SchemaProperty>, prefix: string, parentSection?: string) {
    const entries = Object.entries(props).sort(
      ([, a], [, b]) => (a["x-order"] ?? 999) - (b["x-order"] ?? 999)
    );

    for (const [key, prop] of entries) {
      const fullKey = prefix ? `${prefix}.${key}` : key;
      const section = prop["x-section"] ?? parentSection ?? "General";

      // Skip global-only fields in project config
      if (!isGlobal && prop["x-global-only"]) continue;

      // Recurse into nested objects that have properties (but not additionalProperties — those are key-value maps)
      if (prop.type === "object" && prop.properties && !prop.additionalProperties) {
        processProperties(prop.properties, fullKey, section);
        continue;
      }

      fields.push({
        key: fullKey,
        label: prop.title ?? key,
        description: prop.description,
        type: inferFieldType(prop),
        options: (prop.enum ?? prop.items?.enum)?.map(String),
        placeholder: prop["x-placeholder"],
        section,
        step: prop["x-step"],
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
  if (prop.type === "array") return "array";
  if (prop.type === "object" && prop.additionalProperties) return "keyvalue";
  return "text";
}

// ---------------------------------------------------------------------------
// SVG icon helpers (avoid repeating markup)
// ---------------------------------------------------------------------------

const XIcon = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
  </svg>
);

const PlusIcon = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
  </svg>
);

// ---------------------------------------------------------------------------
// Value helpers
// ---------------------------------------------------------------------------

function getNestedValue(obj: Record<string, any>, path: string): any {
  let cur: any = obj;
  for (const p of path.split(".")) {
    if (cur == null || typeof cur !== "object") return undefined;
    cur = cur[p];
  }
  return cur;
}

function setNestedValue(obj: Record<string, any>, path: string, value: any): Record<string, any> {
  const idx = path.indexOf(".");
  const clone = { ...obj };
  if (idx === -1) { clone[path] = value; return clone; }
  const key = path.slice(0, idx);
  const rest = path.slice(idx + 1);
  clone[key] = setNestedValue(typeof clone[key] === "object" && clone[key] !== null ? { ...clone[key] } : {}, rest, value);
  return clone;
}

function cleanForSerialize(obj: any): any {
  if (obj === null || obj === undefined) return undefined;
  if (Array.isArray(obj)) { const f = obj.filter((v) => v != null && v !== ""); return f.length ? f : undefined; }
  if (typeof obj === "object") {
    const r: Record<string, any> = {};
    let has = false;
    for (const [k, v] of Object.entries(obj)) { const c = cleanForSerialize(v); if (c !== undefined) { r[k] = c; has = true; } }
    return has ? r : undefined;
  }
  return obj === "" || obj === false ? undefined : obj;
}

// ---------------------------------------------------------------------------
// Shared small components
// ---------------------------------------------------------------------------

function RemoveBtn({ onClick, colors }: { onClick: () => void; colors: ColorPalette }) {
  return (
    <button
      onClick={onClick}
      style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 2, lineHeight: 1, display: "flex", alignItems: "center", flexShrink: 0 }}
      onMouseEnter={(e) => { e.currentTarget.style.color = colors.error; }}
      onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; }}
    >
      <XIcon />
    </button>
  );
}

function AddBtn({ onClick, colors }: { onClick: () => void; colors: ColorPalette }) {
  return (
    <button
      onClick={onClick}
      style={{ background: "none", border: `1px solid ${colors.border}`, borderRadius: 6, color: colors.textDim, cursor: "pointer", padding: "4px 8px", lineHeight: 1, display: "flex", alignItems: "center", flexShrink: 0 }}
      onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; }}
      onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; }}
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
  fontSize: 12, fontFamily: fonts.mono, backgroundColor: colors.surface, borderRadius: 4, padding: "3px 8px",
  overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
});

const rowBorder = (colors: ColorPalette): React.CSSProperties => ({ borderBottom: `1px solid ${colors.border}` });

// ---------------------------------------------------------------------------
// ConfigForm
// ---------------------------------------------------------------------------

export function ConfigForm({ schema, config, onSave, isGlobal, title, colors, onDirtyChange }: ConfigFormProps) {
  const [viewMode, setViewMode] = useState<"form" | "json">("form");
  const [formData, setFormData] = useState<Record<string, any>>({});
  const [jsonDraft, setJsonDraft] = useState("");
  const [dirty, setDirtyRaw] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const setDirty = (v: boolean) => { setDirtyRaw(v); onDirtyChange?.(v); };

  useEffect(() => {
    if (!config) { setFormData({}); setJsonDraft("{\n  \n}\n"); setDirty(false); return; }
    const parsed = config.content ?? {};
    setFormData(parsed);
    setJsonDraft(config.raw ?? JSON.stringify(parsed, null, 2) + "\n");
    setDirty(false); setError(null);
  }, [config]);

  const switchView = useCallback((mode: "form" | "json") => {
    if (mode === viewMode) return;
    if (viewMode === "form") { setJsonDraft(JSON.stringify(cleanForSerialize(formData) ?? {}, null, 2) + "\n"); }
    else {
      try { setFormData(JSON.parse(jsonDraft)); setError(null); }
      catch (e: any) { setError("Invalid JSON: " + (e.message ?? "parse error")); return; }
    }
    setViewMode(mode);
  }, [viewMode, formData, jsonDraft]);

  const handleFormChange = useCallback((key: string, value: any) => {
    setFormData((prev) => setNestedValue(prev, key, value));
    setDirty(true);
  }, []);

  const handleSave = useCallback(async () => {
    setSaving(true); setError(null);
    const content = viewMode === "json" ? jsonDraft : JSON.stringify(cleanForSerialize(formData) ?? {}, null, 2) + "\n";
    const err = await onSave(content);
    setSaving(false);
    if (err) setError(err); else setDirty(false);
  }, [viewMode, formData, jsonDraft, onSave]);

  const handleCancel = useCallback(() => {
    if (!config) { setFormData({}); setJsonDraft("{\n  \n}\n"); }
    else {
      const parsed = config.content ?? {};
      setFormData(parsed);
      setJsonDraft(config.raw ?? JSON.stringify(parsed, null, 2) + "\n");
    }
    setDirty(false); setError(null);
  }, [config]);

  const fields = schema ? schemaToFields(schema, isGlobal) : [];
  const sections: { name: string; fields: FieldDef[] }[] = [];
  const map = new Map<string, FieldDef[]>();
  for (const f of fields) {
    if (!map.has(f.section)) { const a: FieldDef[] = []; map.set(f.section, a); sections.push({ name: f.section, fields: a }); }
    map.get(f.section)!.push(f);
  }

  const inputStyle: React.CSSProperties = {
    backgroundColor: colors.bg, border: `1px solid ${colors.border}`, borderRadius: 6,
    padding: "5px 8px", fontSize: 12, fontFamily: fonts.mono, color: colors.text, outline: "none", boxSizing: "border-box",
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
    <div style={{ marginTop: 20 }}>
      {/* Header: title + pill toggle */}
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>{title}</div>
        <div style={{ display: "flex", border: `1px solid ${colors.border}`, borderRadius: 6, overflow: "hidden" }}>
          {(["form", "json"] as const).map((m) => (
            <button key={m} onClick={() => switchView(m)} style={{
              padding: "3px 10px", fontSize: 11, fontWeight: 500, fontFamily: "inherit", border: "none", cursor: "pointer",
              backgroundColor: viewMode === m ? colors.pillActiveBg : "transparent",
              color: viewMode === m ? colors.pillActiveText : colors.textDim,
              transition: "background-color 0.15s, color 0.15s",
            }}>
              {m === "form" ? "Form" : "JSON"}
            </button>
          ))}
        </div>
      </div>

      <div style={{ fontSize: 11, fontFamily: fonts.mono, color: colors.textDim, marginBottom: 8, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {config?.path}
      </div>

      {viewMode === "json" ? (
        <JSONView colors={colors} draft={jsonDraft} onChange={(v) => { setJsonDraft(v); setDirty(true); }} textareaRef={textareaRef} onSave={handleSave} error={error} />
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          {sections.map((sec) => (
            <div key={sec.name}>
              <div style={{ fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.8, marginBottom: 6 }}>{sec.name}</div>
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

      <div style={{ display: "flex", gap: 8, marginTop: 10, justifyContent: "flex-end" }}>
        <button onClick={handleCancel} disabled={!dirty} style={{
          padding: "5px 12px", backgroundColor: "transparent", border: `1px solid ${colors.border}`, borderRadius: 6,
          color: dirty ? colors.text : colors.textDim, fontSize: 12, cursor: dirty ? "pointer" : "default", fontFamily: "inherit", opacity: dirty ? 1 : 0.5,
        }}>Cancel</button>
        <button onClick={handleSave} disabled={saving || !dirty} style={{
          padding: "5px 12px", backgroundColor: colors.active, border: "none", borderRadius: 6,
          color: colors.white, fontSize: 12, fontWeight: 500, cursor: saving || !dirty ? "default" : "pointer", opacity: saving || !dirty ? 0.6 : 1, fontFamily: "inherit",
        }}>{saving ? "Saving..." : "Save"}</button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// JSON View
// ---------------------------------------------------------------------------

function JSONView({ colors, draft, onChange, textareaRef, onSave, error }: {
  colors: ColorPalette; draft: string; onChange: (v: string) => void;
  textareaRef: React.RefObject<HTMLTextAreaElement | null>; onSave: () => void; error: string | null;
}) {
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "s") { e.preventDefault(); onSave(); }
    if (e.key === "Tab") {
      e.preventDefault();
      const ta = textareaRef.current; if (!ta) return;
      const s = ta.selectionStart, end = ta.selectionEnd;
      onChange(draft.substring(0, s) + "  " + draft.substring(end));
      setTimeout(() => { ta.selectionStart = ta.selectionEnd = s + 2; }, 0);
    }
  };
  return (
    <div>
      <textarea ref={textareaRef} value={draft} onChange={(e) => onChange(e.target.value)} onKeyDown={handleKeyDown} spellCheck={false} style={{
        width: "100%", minHeight: 200, maxHeight: 500, backgroundColor: colors.bg,
        border: `1px solid ${error ? colors.error : colors.border}`, borderRadius: 8, padding: "10px 12px",
        fontSize: 12, fontFamily: fonts.mono, color: colors.text, lineHeight: 1.5, resize: "vertical", outline: "none", boxSizing: "border-box",
      }} />
      <div style={{ fontSize: 10, color: colors.textDim, marginTop: 4, textAlign: "right" }}>
        {navigator.platform.includes("Mac") ? "\u2318S" : "Ctrl+S"} to save
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Field Renderer (dispatcher)
// ---------------------------------------------------------------------------

function FieldRenderer({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: any; onChange: (v: any) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
  switch (field.type) {
    case "text": return <TextFieldRow field={field} value={value ?? ""} onChange={onChange} colors={colors} inputStyle={inputStyle} />;
    case "password": return <PasswordFieldRow field={field} value={value ?? ""} onChange={onChange} colors={colors} inputStyle={inputStyle} />;
    case "number": return <NumberFieldRow field={field} value={value} onChange={onChange} colors={colors} inputStyle={inputStyle} />;
    case "toggle": return <ToggleFieldRow field={field} value={!!value} onChange={onChange} colors={colors} />;
    case "dropdown": return <DropdownFieldRow field={field} value={value ?? ""} onChange={onChange} colors={colors} inputStyle={inputStyle} />;
    case "array": return <ArrayFieldRow field={field} value={value ?? []} onChange={onChange} colors={colors} inputStyle={inputStyle} />;
    case "multiselect": return <MultiSelectFieldRow field={field} value={value ?? []} onChange={onChange} colors={colors} />;
    case "keyvalue": return <KeyValueFieldRow field={field} value={value ?? {}} onChange={onChange} colors={colors} inputStyle={inputStyle} />;
    default: return null;
  }
}

// ---------------------------------------------------------------------------
// Text
// ---------------------------------------------------------------------------

function TextFieldRow({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
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

function PasswordFieldRow({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
  const [show, setShow] = useState(false);
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ display: "flex", alignItems: "center", gap: 4, flexShrink: 0 }}>
        <input type={show ? "text" : "password"} value={value} onChange={(e) => onChange(e.target.value)} placeholder={field.placeholder} style={{ ...inputStyle, width: 160 }} />
        <button onClick={() => setShow(!show)} title={show ? "Hide" : "Show"} style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 4, lineHeight: 1, display: "flex", alignItems: "center" }}>
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

function NumberFieldRow({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: any; onChange: (v: any) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <input type="number" value={value ?? ""} step={field.step} placeholder={field.placeholder}
        onChange={(e) => { const r = e.target.value; if (r === "") { onChange(undefined); return; } const n = Number(r); onChange(isNaN(n) ? undefined : n); }}
        style={{ ...inputStyle, width: 100, flexShrink: 0 }} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Toggle (matches Settings.tsx ToggleRow switch)
// ---------------------------------------------------------------------------

function ToggleFieldRow({ field, value, onChange, colors }: {
  field: FieldDef; value: boolean; onChange: (v: boolean) => void; colors: ColorPalette;
}) {
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

function DropdownFieldRow({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: string; onChange: (v: string) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, padding: "8px 12px", ...rowBorder(colors) }}>
      <FieldLabel field={field} colors={colors} />
      <select value={value} onChange={(e) => onChange(e.target.value)} style={{ ...inputStyle, width: 180, flexShrink: 0, cursor: "pointer" }}>
        {(field.options ?? []).map((o) => <option key={o} value={o}>{o || "(default)"}</option>)}
      </select>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Array
// ---------------------------------------------------------------------------

function MultiSelectFieldRow({ field, value, onChange, colors }: {
  field: FieldDef; value: string[]; onChange: (v: string[]) => void; colors: ColorPalette;
}) {
  const options = field.options ?? [];
  const selected = new Set(Array.isArray(value) ? value : []);
  const toggle = (opt: string) => {
    const next = new Set(selected);
    if (next.has(opt)) next.delete(opt); else next.add(opt);
    onChange([...next]);
  };
  return (
    <div style={{ padding: "8px 12px", ...rowBorder(colors), display: "flex", alignItems: "center", justifyContent: "space-between" }}>
      <FieldLabel field={field} colors={colors} />
      <div style={{ display: "flex", gap: 6 }}>
        {options.map((opt) => (
          <button key={opt} onClick={() => toggle(opt)} style={{
            padding: "3px 10px", fontSize: 12, borderRadius: 4, cursor: "pointer", fontFamily: "inherit",
            backgroundColor: selected.has(opt) ? colors.active : "transparent",
            color: selected.has(opt) ? colors.white : colors.textDim,
            border: `1px solid ${selected.has(opt) ? colors.active : colors.border}`,
          }}>{opt}</button>
        ))}
      </div>
    </div>
  );
}

function ArrayFieldRow({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: any[]; onChange: (v: any[]) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
  const [draft, setDraft] = useState("");
  const items: string[] = Array.isArray(value) ? value : [];
  const add = () => { const t = draft.trim(); if (!t) return; onChange([...items, t]); setDraft(""); };

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
        <input type="text" value={draft} onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }}
          placeholder={field.placeholder ?? "Add item..."} style={{ ...inputStyle, flex: 1 }} />
        <AddBtn onClick={add} colors={colors} />
        {(field.key === "extra_dirs" || field.key === "memory.paths") && window.loopAPI?.showOpenDirectoryDialog && (
          <button onClick={async () => {
            const dir = await window.loopAPI?.showOpenDirectoryDialog?.();
            if (dir) onChange([...items, dir]);
          }} style={{ background: "none", border: `1px solid ${colors.border}`, borderRadius: 6, color: colors.textDim, cursor: "pointer", padding: "3px 8px", fontSize: 11, fontFamily: "inherit", whiteSpace: "nowrap" }}
          onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textDim; }}
          onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; }}
          >Browse...</button>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Key-Value
// ---------------------------------------------------------------------------

function KeyValueFieldRow({ field, value, onChange, colors, inputStyle }: {
  field: FieldDef; value: Record<string, string>; onChange: (v: Record<string, string>) => void; colors: ColorPalette; inputStyle: React.CSSProperties;
}) {
  const [dk, setDk] = useState("");
  const [dv, setDv] = useState("");
  const entries = Object.entries(value ?? {});
  const add = () => { const k = dk.trim(); if (!k) return; onChange({ ...value, [k]: dv }); setDk(""); setDv(""); };

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
              <RemoveBtn onClick={() => { const n = { ...value }; delete n[k]; onChange(n); }} colors={colors} />
            </div>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 6 }}>
        <input type="text" value={dk} onChange={(e) => setDk(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }}
          placeholder="KEY" style={{ ...inputStyle, width: 100 }} />
        <span style={{ fontSize: 12, color: colors.textDim }}>=</span>
        <input type="text" value={dv} onChange={(e) => setDv(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }}
          placeholder="value" style={{ ...inputStyle, flex: 1 }} />
        <AddBtn onClick={add} colors={colors} />
      </div>
    </div>
  );
}
