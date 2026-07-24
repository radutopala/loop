import { useCallback, useEffect, useState } from "react";
import { fetchAgentConfig, updateAgentConfig } from "../../api/channels";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { logErr } from "../../utils/log";

// Mirrors the config schema's claude_model options (internal/config/schema.go);
// older/niche ids (opus-4-7, opus-4-6[1m], sonnet-4-6, …) go through the
// custom input below.
const MODEL_PRESETS = ["claude-opus-5", "claude-fable-5", "claude-opus-4-8", "claude-sonnet-5", "claude-haiku-4-5"];
const EFFORT_PRESETS = ["low", "medium", "high", "xhigh", "max"];

/** Strip the common "claude-" prefix so the pill stays compact. */
function shortModel(model: string): string {
  return model.replace(/^claude-/, "");
}

/**
 * Composer pill for the per-channel model/effort override. Applies to any
 * channel/thread/worktree: selections PATCH immediately and take effect on the
 * channel's next agent run; "Default" clears back to the config value (shown
 * in parentheses so the fallback is concrete).
 */
export function AgentConfigPill({ channelId }: { channelId: string }) {
  const { colors } = useTheme();
  const [open, setOpen] = useState(false);
  const [model, setModel] = useState("");
  const [effort, setEffort] = useState("");
  const [defaults, setDefaults] = useState({ model: "", effort: "" });
  const [customModel, setCustomModel] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetchAgentConfig(channelId)
      .then((cfg) => {
        if (cancelled) return;
        setModel(cfg.model);
        setEffort(cfg.effort);
        setDefaults({ model: cfg.default_model, effort: cfg.default_effort });
      })
      .catch(logErr("fetching agent config"));
    return () => {
      cancelled = true;
    };
  }, [channelId]);

  const apply = useCallback(
    async (nextModel: string, nextEffort: string) => {
      setError(null);
      const prev = { model, effort };
      setModel(nextModel);
      setEffort(nextEffort);
      try {
        await updateAgentConfig(channelId, nextModel, nextEffort);
      } catch (e) {
        setModel(prev.model);
        setEffort(prev.effort);
        setError(e instanceof Error ? e.message : "Failed to update");
      }
    },
    [channelId, model, effort],
  );

  const overridden = model !== "" || effort !== "";
  const label = overridden ? [model ? shortModel(model) : null, effort || null].filter(Boolean).join(" · ") : "model";

  const rowStyle = (selected: boolean): React.CSSProperties => ({
    display: "flex",
    alignItems: "center",
    gap: 6,
    width: "100%",
    padding: "3px 10px",
    border: "none",
    background: "transparent",
    color: selected ? colors.textLight : colors.textDim,
    cursor: "pointer",
    fontSize: 11,
    fontFamily: fonts.mono,
    textAlign: "left",
  });
  const sectionStyle: React.CSSProperties = {
    padding: "4px 10px 2px",
    fontSize: 9,
    color: colors.textDim,
    textTransform: "uppercase",
    letterSpacing: 1,
  };

  return (
    <div style={{ position: "relative", display: "flex", alignItems: "center", flexShrink: 0, marginRight: 8 }}>
      <button
        onClick={() => setOpen((v) => !v)}
        title={
          overridden
            ? `Model/effort override active for this channel:\n${model || `(default ${defaults.model})`} · ${effort || `(default effort)`}\nApplies from the next run.`
            : `Override the model/effort for this channel's runs (default: ${defaults.model || "config"}${defaults.effort ? ` · ${defaults.effort}` : ""})`
        }
        style={{
          display: "flex",
          alignItems: "center",
          gap: 4,
          height: 24,
          padding: "0 8px",
          background: "transparent",
          border: `1px solid ${overridden ? colors.active : colors.border}`,
          borderRadius: 12,
          color: overridden ? colors.active : colors.textDim,
          cursor: "pointer",
          fontFamily: fonts.mono,
          fontSize: 10,
        }}
      >
        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
          <line x1="4" y1="8" x2="20" y2="8" />
          <circle cx="9" cy="8" r="2.5" fill="currentColor" stroke="none" />
          <line x1="4" y1="16" x2="20" y2="16" />
          <circle cx="15" cy="16" r="2.5" fill="currentColor" stroke="none" />
        </svg>
        {label}
      </button>
      {open && (
        <>
          <div style={{ position: "fixed", inset: 0, zIndex: 999 }} onMouseDown={() => setOpen(false)} />
          <div
            style={{
              position: "absolute",
              bottom: "calc(100% + 6px)",
              right: 0,
              zIndex: 1000,
              minWidth: 240,
              backgroundColor: colors.surface,
              border: `1px solid ${colors.border}`,
              borderRadius: 8,
              boxShadow: `0 4px 12px ${colors.shadow}`,
              padding: "4px 0",
            }}
          >
            <div style={sectionStyle}>Model</div>
            <button style={rowStyle(model === "")} onClick={() => apply("", effort)}>
              <span style={{ width: 12 }}>{model === "" ? "✓" : ""}</span>
              Default{defaults.model ? ` (${defaults.model})` : ""}
            </button>
            {MODEL_PRESETS.map((m) => (
              <button key={m} style={rowStyle(model === m)} onClick={() => apply(m, effort)}>
                <span style={{ width: 12 }}>{model === m ? "✓" : ""}</span>
                {m}
              </button>
            ))}
            {model !== "" && !MODEL_PRESETS.includes(model) && (
              <button style={rowStyle(true)} onClick={() => apply(model, effort)}>
                <span style={{ width: 12 }}>✓</span>
                {model}
              </button>
            )}
            <div style={{ display: "flex", gap: 4, padding: "3px 10px 5px" }}>
              <input
                value={customModel}
                onChange={(e) => setCustomModel(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && customModel.trim()) {
                    void apply(customModel.trim(), effort);
                    setCustomModel("");
                  }
                }}
                placeholder="custom model id…"
                style={{
                  flex: 1,
                  padding: "3px 6px",
                  fontSize: 11,
                  fontFamily: fonts.mono,
                  backgroundColor: colors.bg,
                  border: `1px solid ${colors.border}`,
                  borderRadius: 4,
                  color: colors.text,
                  outline: "none",
                }}
              />
            </div>
            <div style={{ borderTop: `1px solid ${colors.border}`, margin: "2px 0" }} />
            <div style={sectionStyle}>Effort</div>
            <button
              style={rowStyle(effort === "")}
              onClick={() => apply(model, "")}
              title={defaults.effort ? `From config: ${defaults.effort}` : "No claude_effort in config — the CLI uses the selected model's own default effort"}
            >
              <span style={{ width: 12 }}>{effort === "" ? "✓" : ""}</span>
              Default ({defaults.effort || "model default"})
            </button>
            {EFFORT_PRESETS.map((ef) => (
              <button key={ef} style={rowStyle(effort === ef)} onClick={() => apply(model, ef)}>
                <span style={{ width: 12 }}>{effort === ef ? "✓" : ""}</span>
                {ef}
              </button>
            ))}
            {error && <div style={{ padding: "4px 10px", fontSize: 10, color: colors.warning }}>{error}</div>}
            <div style={{ padding: "4px 10px 2px", fontSize: 9, color: colors.textDim }}>Applies to this channel's next run</div>
          </div>
        </>
      )}
    </div>
  );
}
