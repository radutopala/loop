import type { ColorPalette } from "../../theme";
import type { WorkflowDef } from "../../api/loopApi";
import { buildBtnStyle, buildBtnSecondaryStyle, buildInputStyle } from "../../utils/workflowHelpers";

interface WorkflowStartDialogProps {
  show: boolean;
  definitions: WorkflowDef[];
  startWorkflowName: string;
  startInputs: Record<string, string>;
  selectedStartDef: WorkflowDef | undefined;
  colors: ColorPalette;
  onClose: () => void;
  onSelectWorkflow: (name: string) => void;
  onInputChange: (fn: (prev: Record<string, string>) => Record<string, string>) => void;
  onStart: () => void;
  testId?: string;
}

export function WorkflowStartDialog({
  show,
  definitions,
  startWorkflowName,
  startInputs,
  selectedStartDef,
  colors,
  onClose,
  onSelectWorkflow,
  onInputChange,
  onStart,
  testId,
}: WorkflowStartDialogProps) {
  if (!show) return null;

  const btnStyle = buildBtnStyle(colors);
  const btnSecondaryStyle = buildBtnSecondaryStyle(colors);
  const inputStyle = buildInputStyle(colors);

  return (
    <div
      data-testid={testId}
      style={{ position: "fixed", inset: 0, zIndex: 9999, display: "flex", alignItems: "center", justifyContent: "center", backgroundColor: "rgba(0,0,0,0.5)" }}
      onClick={onClose}
    >
      <div
        style={{ backgroundColor: colors.surface, borderRadius: 12, padding: "20px 24px", maxWidth: 420, width: "90%", boxShadow: "0 8px 32px rgba(0,0,0,0.3)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, marginBottom: 12 }}>Start Workflow</div>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <select
            data-testid="workflow-start-select"
            value={startWorkflowName}
            onChange={(e) => onSelectWorkflow(e.target.value)}
            style={{ ...inputStyle, cursor: "pointer" }}
          >
            {definitions.map((d) => (
              <option key={d.name} value={d.name}>{d.name} — {d.description}</option>
            ))}
          </select>
          {selectedStartDef?.inputs && Object.entries(selectedStartDef.inputs).map(([key, input]) => (
            <div key={key} style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <label style={{ fontSize: 11, color: colors.textDim }}>
                {key}{input.required ? " *" : ""}{input.description ? ` — ${input.description}` : ""}
              </label>
              <input
                type="text"
                value={startInputs[key] ?? ""}
                onChange={(e) => onInputChange((prev) => ({ ...prev, [key]: e.target.value }))}
                style={inputStyle}
              />
            </div>
          ))}
          <div style={{ display: "flex", gap: 6, justifyContent: "flex-end", marginTop: 4 }}>
            <button onClick={onClose} style={btnSecondaryStyle}>Cancel</button>
            <button onClick={onStart} style={btnStyle}>Start</button>
          </div>
        </div>
      </div>
    </div>
  );
}
