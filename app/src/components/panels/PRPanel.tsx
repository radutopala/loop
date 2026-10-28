import { useState } from "react";
import { cancelMerge, fixPR, getPRCheckLog, mergePR, type PRFailedCheck, type PRStatus, rerunPRChecks, setPRAutoFix } from "../../api/pr";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import {
  type PRTone,
  prAutoFixLabel,
  prAutoFixToggle,
  prCanFix,
  prCanRerun,
  prChip,
  prConflictText,
  prLabel,
  prMergeActions,
  prMergeMethod,
  prReasonText,
  prStaleCheckout,
} from "../../utils/prStatus";

export function prToneColor(tone: PRTone, colors: ColorPalette): string {
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
 * The Git panel's PR/MR tab for the thread's PR/MR: its checks, why auto-fix
 * did what it did, the failed checks linking to the forge with their logs on
 * demand, the auto-fix toggle, "Fix it", "Re-run failed", and the merge
 * buttons. onChanged refetches the status after an action.
 */
export function PRPanel({ channelId, status, colors, onChanged }: { channelId: string; status: PRStatus; colors: ColorPalette; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [logs, setLogs] = useState<Record<string, LogView>>({});
  const chip = prChip(status);
  const merge = prMergeActions(status);
  const method = prMergeMethod(status);
  const conflict = prConflictText(status);
  const reason = prReasonText(status);
  const failed = status.failed ?? [];
  const run = (action: () => Promise<void>) => {
    setBusy(true);
    setError("");
    action()
      .then(onChanged)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setBusy(false));
  };
  const toggleLog = (f: PRFailedCheck) => {
    if (logs[f.id]) {
      setLogs(({ [f.id]: _, ...rest }) => rest);
      return;
    }
    setLogs((l) => ({ ...l, [f.id]: { loading: true } }));
    getPRCheckLog(channelId, f.id)
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
  const open = !status.merged && !status.closed;
  const { total, passed, failed: nFailed, pending } = status.checks;
  return (
    <div data-testid="git-panel-pr" style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column", gap: 12, padding: "10px 12px", fontSize: 12, color: colors.text }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <a
          href={status.url}
          onClick={(e) => openLink(e, status.url)}
          data-testid="git-panel-pr-chip"
          data-tone={chip.tone}
          style={{ color: prToneColor(chip.tone, colors), fontFamily: fonts.mono, textDecoration: "none", fontSize: 13 }}
        >
          {prLabel(status)} · {chip.text}
        </a>
        {total > 0 && (
          <span style={{ color: colors.textDim, fontSize: 11 }}>
            {passed}/{total} passed{nFailed > 0 && ` · ${nFailed} failed`}
            {pending > 0 && ` · ${pending} running`}
            {total - passed - nFailed - pending > 0 && ` · ${total - passed - nFailed - pending} skipped`}
          </span>
        )}
        {status.head_sha && <span style={{ color: colors.textDim, fontSize: 11 }}>head {sha(status.head_sha)}</span>}
        {!!status.unresolved_threads && (
          <a href={status.url} onClick={(e) => openLink(e, status.url)} data-testid="git-panel-pr-threads" style={{ color: colors.warning, fontSize: 11, textDecoration: "none" }}>
            {status.unresolved_threads} unresolved review thread{status.unresolved_threads > 1 ? "s" : ""}
          </a>
        )}
        {prStaleCheckout(status) && (
          <span data-testid="git-panel-pr-stale" style={{ color: colors.warning, fontSize: 11 }}>
            The checkout is at {sha(status.checkout_sha ?? "")}, not the head: it was pushed from elsewhere, or the checkout has commits not pushed yet.
          </span>
        )}
        {conflict && (
          <span data-testid="git-panel-pr-conflict" style={{ color: colors.error, fontSize: 11 }}>
            {conflict}
          </span>
        )}
        {reason && (
          <span data-testid="git-panel-pr-reason" style={{ color: colors.textLight, fontSize: 11 }}>
            {reason}
          </span>
        )}
        {status.attempts > 0 && (
          <span style={{ color: colors.textDim, fontSize: 11 }}>
            Auto-fix attempts: {status.attempts}/{status.max_attempts}
          </span>
        )}
      </div>

      {open && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap" }}>
          <label
            title="Send failed checks on the agent's own commits back to it to fix. Merge conflicts and review threads are left to you."
            style={{ display: "inline-flex", alignItems: "center", gap: 4, color: colors.textDim, cursor: "pointer", fontSize: 11, marginRight: 4 }}
          >
            <input type="checkbox" checked={status.auto_fix} disabled={busy} onChange={() => run(() => setPRAutoFix(channelId, prAutoFixToggle(status)))} />
            {prAutoFixLabel(status)}
          </label>
          {prCanFix(status) && (
            <button style={btn} disabled={busy} onClick={() => run(() => fixPR(channelId))} title="Send the failure to the agent now">
              Fix it
            </button>
          )}
          {prCanRerun(status) && (
            <button style={btn} disabled={busy} onClick={() => run(() => rerunPRChecks(channelId))} title="Re-run the failed checks on the forge">
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
                  <button data-testid="git-panel-pr-log-toggle" style={{ ...btn, cursor: "pointer", opacity: 1, fontSize: 10, padding: "1px 6px" }} onClick={() => toggleLog(f)}>
                    {log ? "Hide log" : "Log"}
                  </button>
                </div>
                {log && (
                  <pre
                    data-testid="git-panel-pr-log"
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
