import { useState } from "react";
import { type CIStatus, cancelMerge, fixCI, mergePR, setCIAutoFix } from "../../api/ci";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import { type CITone, ciAutoFixLabel, ciAutoFixToggle, ciCanFix, ciChip, ciLabel, ciMergeActions, ciMergeMethod } from "../../utils/ciState";

function toneColor(tone: CITone, colors: ColorPalette): string {
  switch (tone) {
    case "ok":
      return "#3fb950";
    case "failed":
      return colors.error;
    case "running":
      return colors.warning;
    case "merging":
      return colors.active;
    case "merged":
      return "#a371f7";
    default:
      return colors.textDim;
  }
}

function openLink(e: React.MouseEvent, url?: string) {
  e.preventDefault();
  if (url) window.open(url, "_blank", "noopener,noreferrer");
}

/**
 * The Git panel's CI row for the thread's PR/MR: its checks, the failed
 * ones linking to the forge, the auto-fix toggle, "Fix it", and the merge
 * buttons. onChanged refetches the CI after an action.
 */
export function CISection({ channelId, ci, colors, onChanged }: { channelId: string; ci: CIStatus; colors: ColorPalette; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const chip = ciChip(ci);
  const merge = ciMergeActions(ci);
  const method = ciMergeMethod(ci);
  const run = (action: () => Promise<void>) => {
    setBusy(true);
    setError("");
    action()
      .then(onChanged)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };
  const btn: React.CSSProperties = {
    background: "none",
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.textLight,
    cursor: busy ? "default" : "pointer",
    fontSize: 10,
    padding: "1px 6px",
    opacity: busy ? 0.6 : 1,
  };
  const withMethod = (label: string) => (method ? `${label} (${method})` : label);
  const failed = ci.failed ?? [];
  return (
    <div data-testid="git-panel-ci" style={{ display: "flex", flexDirection: "column", gap: 4, padding: "4px 12px", borderBottom: `1px solid ${colors.border}`, fontSize: 11, flexShrink: 0 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
        <a
          href={ci.url}
          onClick={(e) => openLink(e, ci.url)}
          data-testid="git-panel-ci-chip"
          data-tone={chip.tone}
          style={{ color: toneColor(chip.tone, colors), fontFamily: fonts.mono, textDecoration: "none", flexShrink: 0 }}
        >
          {ciLabel(ci)} · {chip.text}
        </a>
        <div style={{ flex: 1 }} />
        {!ci.merged && !ci.closed && (
          <label title="Send failures on the agent's commits back to it" style={{ display: "inline-flex", alignItems: "center", gap: 4, color: colors.textDim, cursor: "pointer" }}>
            <input type="checkbox" checked={ci.auto_fix} disabled={busy} onChange={() => run(() => setCIAutoFix(channelId, ciAutoFixToggle(ci)))} />
            {ciAutoFixLabel(ci)}
          </label>
        )}
        {ciCanFix(ci) && (
          <button style={btn} disabled={busy} onClick={() => run(() => fixCI(channelId))} title="Send the failure to the agent now">
            Fix it
          </button>
        )}
        {merge.whenReady && (
          <button style={btn} disabled={busy} onClick={() => run(() => mergePR(channelId, true))} title="Merge once the checks pass">
            {withMethod("Merge when ready")}
          </button>
        )}
        {merge.cancel && (
          <button style={btn} disabled={busy} onClick={() => run(() => cancelMerge(channelId))}>
            {withMethod("Cancel auto-merge")}
          </button>
        )}
        {merge.now && (
          <button style={btn} disabled={busy} onClick={() => run(() => mergePR(channelId, false))}>
            {withMethod("Merge now")}
          </button>
        )}
      </div>
      {failed.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
          {failed.map((f) => (
            <a key={f.id} href={f.url} onClick={(e) => openLink(e, f.url)} style={{ color: colors.textLight, textDecoration: "none", fontFamily: fonts.mono }}>
              <span style={{ color: colors.error }}>✗</span> {f.group ? `${f.group} / ${f.name}` : f.name}
              {f.infra && <span style={{ color: colors.textDim }}> (runner)</span>}
            </a>
          ))}
          {ci.attempts > 0 && (
            <span style={{ color: colors.textDim }}>
              auto-fix attempts: {ci.attempts}/{ci.max_attempts}
            </span>
          )}
        </div>
      )}
      {error && <span style={{ color: colors.error }}>{error}</span>}
    </div>
  );
}
