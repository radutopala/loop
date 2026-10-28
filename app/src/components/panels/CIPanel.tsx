import { useState } from "react";
import { type CIFailedCheck, type CIStatus, cancelMerge, fixCI, getCILog, mergePR, rerunCI, setCIAutoFix } from "../../api/ci";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import { type CITone, ciAutoFixLabel, ciAutoFixToggle, ciCanFix, ciCanRerun, ciChip, ciLabel, ciMergeActions, ciMergeMethod, ciReasonText, ciStaleCheckout } from "../../utils/ciState";

export function ciToneColor(tone: CITone, colors: ColorPalette): string {
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

/** A failed check's log as the panel holds it: loading, its tail, or why not. */
type LogView = { loading: true } | { loading: false; text: string; error?: boolean };

/**
 * The Git panel's CI tab for the thread's PR/MR: its checks, why auto-fix
 * did what it did, the failed checks linking to the forge with their logs on
 * demand, the auto-fix toggle, "Fix it", "Re-run failed", and the merge
 * buttons. onChanged refetches the CI after an action.
 */
export function CIPanel({ channelId, ci, colors, onChanged }: { channelId: string; ci: CIStatus; colors: ColorPalette; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [logs, setLogs] = useState<Record<string, LogView>>({});
  const chip = ciChip(ci);
  const merge = ciMergeActions(ci);
  const method = ciMergeMethod(ci);
  const reason = ciReasonText(ci);
  const failed = ci.failed ?? [];
  const run = (action: () => Promise<void>) => {
    setBusy(true);
    setError("");
    action()
      .then(onChanged)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };
  const toggleLog = (f: CIFailedCheck) => {
    if (logs[f.id]) {
      setLogs(({ [f.id]: _, ...rest }) => rest);
      return;
    }
    setLogs((l) => ({ ...l, [f.id]: { loading: true } }));
    getCILog(channelId, f.id)
      .then((text) => setLogs((l) => ({ ...l, [f.id]: { loading: false, text: text || "(empty log)" } })))
      .catch((err: unknown) => setLogs((l) => ({ ...l, [f.id]: { loading: false, text: err instanceof Error ? err.message : String(err), error: true } })));
  };
  const btn: React.CSSProperties = {
    background: "none",
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.textLight,
    cursor: busy ? "default" : "pointer",
    fontSize: 11,
    padding: "2px 8px",
    opacity: busy ? 0.6 : 1,
  };
  const heading: React.CSSProperties = { fontSize: 10, fontWeight: 700, textTransform: "uppercase", letterSpacing: 1, color: colors.textDim };
  const sha = (s: string) => <span style={{ fontFamily: fonts.mono, color: colors.textLight }}>{s.slice(0, 7)}</span>;
  const withMethod = (label: string) => (method ? `${label} (${method})` : label);
  const open = !ci.merged && !ci.closed;
  const { total, passed, failed: nFailed, pending } = ci.checks;
  return (
    <div data-testid="git-panel-ci" style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column", gap: 12, padding: "10px 12px", fontSize: 12, color: colors.text }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <a
          href={ci.url}
          onClick={(e) => openLink(e, ci.url)}
          data-testid="git-panel-ci-chip"
          data-tone={chip.tone}
          style={{ color: ciToneColor(chip.tone, colors), fontFamily: fonts.mono, textDecoration: "none", fontSize: 13 }}
        >
          {ciLabel(ci)} · {chip.text}
        </a>
        {total > 0 && (
          <span style={{ color: colors.textDim, fontSize: 11 }}>
            {passed}/{total} passed{nFailed > 0 && ` · ${nFailed} failed`}
            {pending > 0 && ` · ${pending} running`}
            {total - passed - nFailed - pending > 0 && ` · ${total - passed - nFailed - pending} skipped`}
          </span>
        )}
        {ci.head_sha && <span style={{ color: colors.textDim, fontSize: 11 }}>head {sha(ci.head_sha)}</span>}
        {ciStaleCheckout(ci) && (
          <span data-testid="git-panel-ci-stale" style={{ color: colors.warning, fontSize: 11 }}>
            The checkout is at {sha(ci.checkout_sha ?? "")}, not the head: it was pushed from elsewhere, or the checkout has commits not pushed yet.
          </span>
        )}
        {reason && (
          <span data-testid="git-panel-ci-reason" style={{ color: colors.textLight, fontSize: 11 }}>
            {reason}
          </span>
        )}
        {ci.attempts > 0 && (
          <span style={{ color: colors.textDim, fontSize: 11 }}>
            Auto-fix attempts: {ci.attempts}/{ci.max_attempts}
          </span>
        )}
      </div>

      {open && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
          <label
            title="Send failures on the agent's commits back to it"
            style={{ display: "inline-flex", alignItems: "center", gap: 4, color: colors.textDim, cursor: "pointer", fontSize: 11, marginRight: 4 }}
          >
            <input type="checkbox" checked={ci.auto_fix} disabled={busy} onChange={() => run(() => setCIAutoFix(channelId, ciAutoFixToggle(ci)))} />
            {ciAutoFixLabel(ci)}
          </label>
          {ciCanFix(ci) && (
            <button style={btn} disabled={busy} onClick={() => run(() => fixCI(channelId))} title="Send the failure to the agent now">
              Fix it
            </button>
          )}
          {ciCanRerun(ci) && (
            <button style={btn} disabled={busy} onClick={() => run(() => rerunCI(channelId))} title="Re-run the failed checks on the forge">
              Re-run failed
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
      )}
      {error && <span style={{ color: colors.error, fontSize: 11 }}>{error}</span>}

      {failed.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          <span style={heading}>Failed checks</span>
          {failed.map((f) => {
            const log = logs[f.id];
            return (
              <div key={f.id} style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{ color: colors.error }}>✗</span>
                  <a
                    href={f.url}
                    onClick={(e) => openLink(e, f.url)}
                    style={{ color: colors.textLight, textDecoration: "none", fontFamily: fonts.mono, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                  >
                    {f.group ? `${f.group} / ${f.name}` : f.name}
                  </a>
                  {f.infra && <span style={{ color: colors.textDim }}>(runner)</span>}
                  <div style={{ flex: 1 }} />
                  <button data-testid="git-panel-ci-log-toggle" style={{ ...btn, cursor: "pointer", opacity: 1, fontSize: 10, padding: "1px 6px" }} onClick={() => toggleLog(f)}>
                    {log ? "Hide log" : "Log"}
                  </button>
                </div>
                {log && (
                  <pre
                    data-testid="git-panel-ci-log"
                    style={{
                      margin: 0,
                      padding: 8,
                      maxHeight: 320,
                      overflow: "auto",
                      backgroundColor: colors.bg,
                      border: `1px solid ${colors.border}`,
                      borderRadius: 4,
                      fontFamily: fonts.mono,
                      fontSize: 10,
                      lineHeight: 1.4,
                      whiteSpace: "pre-wrap",
                      wordBreak: "break-all",
                      color: !log.loading && log.error ? colors.error : colors.textLight,
                    }}
                  >
                    {log.loading ? "Loading…" : log.text}
                  </pre>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
