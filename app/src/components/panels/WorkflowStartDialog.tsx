import { useEffect, useState } from "react";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { WorkflowDef } from "../../api/loopApi";
import { buildBtnStyle, buildBtnSecondaryStyle, buildInputStyle } from "../../utils/workflowHelpers";

interface WorkflowStartDialogProps {
  show: boolean;
  startInputs: Record<string, string>;
  selectedStartDef: WorkflowDef | undefined;
  colors: ColorPalette;
  onClose: () => void;
  onInputChange: (fn: (prev: Record<string, string>) => Record<string, string>) => void;
  onStart: () => void;
  onSave: (jsonText: string) => Promise<string | null>;
  testId?: string;
}

// defToJson pretty-prints a definition for the editor, dropping the UI-only
// `scope` tag that isn't part of the stored config.
function defToJson(def: WorkflowDef): string {
  const { scope: _scope, ...rest } = def;
  void _scope;
  return JSON.stringify(rest, null, 2);
}

export function WorkflowStartDialog({
  show,
  startInputs,
  selectedStartDef,
  colors,
  onClose,
  onInputChange,
  onStart,
  onSave,
  testId,
}: WorkflowStartDialogProps) {
  const [configText, setConfigText] = useState("");
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);

  // Reset the editor whenever the dialog opens on a (different) workflow.
  useEffect(() => {
    if (show && selectedStartDef) {
      setConfigText(defToJson(selectedStartDef));
      setSaveError(null);
      setSaved(false);
    }
  }, [show, selectedStartDef?.name]); // eslint-disable-line react-hooks/exhaustive-deps

  // Close on Escape (even while a field/the JSON editor is focused).
  useEffect(() => {
    if (!show) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.stopPropagation(); onClose(); }
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [show, onClose]);

  if (!show || !selectedStartDef) return null;

  const btnStyle = buildBtnStyle(colors);
  const btnSecondaryStyle = buildBtnSecondaryStyle(colors);
  const inputStyle = buildInputStyle(colors);
  const inputEntries = Object.entries(selectedStartDef.inputs ?? {});
  const dirty = configText !== defToJson(selectedStartDef);

  const handleSave = async () => {
    setSaving(true);
    setSaveError(null);
    setSaved(false);
    const err = await onSave(configText);
    setSaving(false);
    if (err) setSaveError(err);
    else setSaved(true);
  };

  const sectionLabel = (label: string) => (
    <div style={{ fontSize: 10, fontWeight: 700, letterSpacing: 1, textTransform: "uppercase", color: colors.textDim, marginBottom: 6 }}>
      {label}
    </div>
  );

  return (
    <div
      data-testid={testId}
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={onClose}
    >
      <div
        style={{
          backgroundColor: colors.surface, borderRadius: 12, padding: "24px 28px",
          maxWidth: 620, width: "92%", maxHeight: "88vh", overflowY: "auto",
          boxShadow: "0 8px 32px rgba(0,0,0,0.35)",
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 12, fontWeight: 700, letterSpacing: 1, textTransform: "uppercase", color: colors.textDim, marginBottom: 14 }}>
          Start Workflow
        </div>

        {/* Selected workflow: name + description (no selector — Run picks it) */}
        <div style={{ marginBottom: 18, paddingBottom: 14, borderBottom: `1px solid ${colors.border}` }}>
          <div style={{ fontSize: 16, fontWeight: 600, color: colors.text, fontFamily: fonts.mono }}>
            {selectedStartDef.name}
          </div>
          {selectedStartDef.description && (
            <div style={{ fontSize: 12.5, color: colors.textDim, marginTop: 5, lineHeight: 1.5 }}>
              {selectedStartDef.description}
            </div>
          )}
        </div>

        {/* Inputs */}
        <div style={{ display: "flex", flexDirection: "column", gap: 14, marginBottom: 18 }}>
          {sectionLabel("Inputs")}
          {inputEntries.length === 0 ? (
            <div style={{ fontSize: 12.5, color: colors.textDim }}>This workflow takes no inputs.</div>
          ) : (
            inputEntries.map(([key, input]) => (
              <div key={key} style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                <label style={{ fontSize: 12, color: colors.text, fontFamily: fonts.mono }}>
                  {key}{input.required ? " *" : ""}
                </label>
                {input.description && (
                  <div style={{ fontSize: 11.5, color: colors.textDim }}>{input.description}</div>
                )}
                <input
                  type="text"
                  value={startInputs[key] ?? ""}
                  placeholder={input.default ? `default: ${input.default}` : ""}
                  onChange={(e) => onInputChange((prev) => ({ ...prev, [key]: e.target.value }))}
                  style={inputStyle}
                />
              </div>
            ))
          )}
        </div>

        {/* Editable full definition */}
        <div style={{ marginBottom: 8 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
            <div style={{ flex: 1 }}>{sectionLabel("Definition (JSON)")}</div>
            {saved && !dirty && <span style={{ fontSize: 11, color: colors.textDim }}>saved ✓</span>}
            <button
              data-testid="workflow-config-save"
              onClick={handleSave}
              disabled={saving || !dirty}
              style={{ ...btnSecondaryStyle, opacity: saving || !dirty ? 0.5 : 1, cursor: saving || !dirty ? "default" : "pointer", padding: "2px 10px", fontSize: 11 }}
            >
              {saving ? "Saving…" : "Save"}
            </button>
          </div>
          <textarea
            data-testid="workflow-config-editor"
            value={configText}
            onChange={(e) => { setConfigText(e.target.value); setSaved(false); }}
            spellCheck={false}
            style={{
              width: "100%", boxSizing: "border-box", minHeight: 220, resize: "vertical",
              background: colors.bg, border: `1px solid ${saveError ? (colors.error ?? "#ef4444") : colors.border}`,
              borderRadius: 6, color: colors.textLight, fontFamily: fonts.mono, fontSize: 11.5,
              lineHeight: 1.5, padding: "8px 10px", outline: "none",
            }}
          />
          {saveError && (
            <div style={{ fontSize: 11.5, color: colors.error ?? "#ef4444", marginTop: 6, whiteSpace: "pre-wrap" }}>
              {saveError}
            </div>
          )}
        </div>

        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 12 }}>
          <button onClick={onClose} style={btnSecondaryStyle}>Cancel</button>
          <button onClick={onStart} style={btnStyle}>Start</button>
        </div>
      </div>
    </div>
  );
}
