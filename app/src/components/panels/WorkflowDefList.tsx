import { useMemo, useState } from "react";
import type { WorkflowDef } from "../../api/workflows";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";

interface WorkflowDefListProps {
  grouped: { global: WorkflowDef[]; project: WorkflowDef[] };
  selectedName: string | null;
  onSelect: (name: string | null) => void;
  onRun: (name: string) => void;
  colors: ColorPalette;
}

/**
 * Grouped, searchable list of workflow definitions (Global / Project), mirroring
 * the Tasks panel. A search box filters by name + description. Selecting a
 * workflow filters the run list to it ("All runs" clears the filter); each row
 * has a "Run" button that starts the workflow immediately (deferring to the
 * start dialog only when it declares a required input).
 */
export function WorkflowDefList({ grouped, selectedName, onSelect, onRun, colors }: WorkflowDefListProps) {
  const [query, setQuery] = useState("");
  const total = grouped.global.length + grouped.project.length;

  const { global, project } = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return grouped;
    const match = (d: WorkflowDef) =>
      d.name.toLowerCase().includes(q) || (d.description ?? "").toLowerCase().includes(q);
    return { global: grouped.global.filter(match), project: grouped.project.filter(match) };
  }, [grouped, query]);

  if (total === 0) return null;

  const row = (name: string, description: string) => {
    const selected = name === selectedName;
    return (
      <div
        key={name}
        data-testid={`workflow-def-${name}`}
        title={description}
        onClick={() => onSelect(selected ? null : name)}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          width: "100%",
          boxSizing: "border-box",
          padding: "5px 10px 5px 12px",
          background: selected ? colors.hoverBg : "transparent",
          borderLeft: `2px solid ${selected ? colors.textLight : "transparent"}`,
          color: selected ? colors.textLight : colors.textDim,
          fontSize: 12,
          fontFamily: fonts.mono,
          cursor: "pointer",
        }}
      >
        <span style={{ flex: 1, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {name}
        </span>
        <button
          data-testid={`workflow-run-${name}`}
          title={`Run ${name} now`}
          onClick={(e) => { e.stopPropagation(); onRun(name); }}
          style={{
            flexShrink: 0,
            background: "transparent",
            border: `1px solid ${colors.border}`,
            color: colors.textLight,
            borderRadius: 4,
            padding: "1px 8px",
            fontSize: 10,
            fontFamily: fonts.mono,
            cursor: "pointer",
          }}
          onMouseEnter={(e) => { e.currentTarget.style.background = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; }}
        >
          ▶ Run
        </button>
      </div>
    );
  };

  const heading = (label: string) => (
    <div
      style={{
        fontSize: 9,
        fontWeight: 700,
        letterSpacing: 1,
        textTransform: "uppercase",
        color: colors.textDim,
        padding: "6px 12px 2px",
        opacity: 0.7,
      }}
    >
      {label}
    </div>
  );

  return (
    <div style={{ borderBottom: `1px solid ${colors.border}`, flexShrink: 0, maxHeight: "62%", display: "flex", flexDirection: "column" }}>
      <div style={{ padding: "6px 8px", flexShrink: 0 }}>
        <input
          data-testid="workflow-search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search workflows…"
          style={{
            width: "100%",
            boxSizing: "border-box",
            background: colors.bg,
            border: `1px solid ${colors.border}`,
            borderRadius: 4,
            color: colors.textLight,
            fontSize: 11,
            fontFamily: fonts.mono,
            padding: "3px 8px",
            outline: "none",
          }}
        />
      </div>
      <div style={{ overflowY: "auto" }}>
        <button
          onClick={() => onSelect(null)}
          style={{
            display: "block",
            width: "100%",
            textAlign: "left",
            padding: "5px 12px",
            background: selectedName === null ? colors.hoverBg : "transparent",
            border: "none",
            borderLeft: `2px solid ${selectedName === null ? colors.textLight : "transparent"}`,
            color: selectedName === null ? colors.textLight : colors.textDim,
            fontSize: 12,
            cursor: "pointer",
          }}
        >
          All runs
        </button>
        {global.length > 0 && heading("Global")}
        {global.map((d) => row(d.name, d.description))}
        {project.length > 0 && heading("Project")}
        {project.map((d) => row(d.name, d.description))}
        {query.trim() !== "" && global.length === 0 && project.length === 0 && (
          <div style={{ padding: "8px 12px", color: colors.textDim, fontSize: 11 }}>
            No workflows match “{query.trim()}”
          </div>
        )}
      </div>
    </div>
  );
}
