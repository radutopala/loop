import { useCallback, useEffect, useState } from "react";
import { fetchProjectTrust, ProjectTrustChangedError, type ProjectTrustStatus, trustProjectConfig } from "../../api/configApi";
import type { ColorPalette } from "../../theme";
import { logErr } from "../../utils/log";
import { UnifiedDiff } from "./UnifiedDiff";

// ProjectTrustNotice shows when the project config's host-reaching fields
// (mounts, extra dirs, gates, envs…) changed since the owner last trusted
// them — an agent can write the file — and lets the owner review and trust
// the change. Until then Loop uses the last trusted version.
export function ProjectTrustNotice({
  channelId,
  colors,
  refreshKey,
  onTrusted,
}: {
  channelId: string;
  colors: ColorPalette;
  refreshKey: unknown;
  /** Called once the config is trusted, so the sidebar and chat drop their notices. */
  onTrusted?: () => void;
}) {
  const [status, setStatus] = useState<ProjectTrustStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const load = useCallback(() => {
    fetchProjectTrust(channelId)
      .then(setStatus)
      .catch((e) => {
        setStatus(null);
        logErr("fetching project trust")(e);
      });
  }, [channelId]);

  // refreshKey re-checks after a save.
  useEffect(() => {
    setMessage(null);
    load();
  }, [load, refreshKey]);

  if (!status || status.trusted) return null;

  const handleTrust = async () => {
    setBusy(true);
    setMessage(null);
    try {
      await trustProjectConfig(channelId, status.hash);
      load();
      onTrusted?.();
    } catch (e: any) {
      setMessage(e instanceof ProjectTrustChangedError ? "The project config changed again. Review the new version." : (e.message ?? "Failed to trust"));
      load();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      data-testid="project-trust-notice"
      style={{
        marginBottom: 12,
        padding: "10px 12px",
        border: `1px solid ${colors.warning}`,
        borderRadius: 6,
        backgroundColor: colors.bg,
        color: colors.text,
        fontSize: 12,
      }}
    >
      <div style={{ fontWeight: 600, color: colors.warning, marginBottom: 4 }}>Project config changed</div>
      <div style={{ color: colors.textMuted, marginBottom: 8 }}>
        Mounts, extra dirs, gates, envs and the other fields that reach your machine changed since you last trusted this project, possibly by an agent.{" "}
        {status.approved ? "Loop uses the last trusted version until you trust this one." : "None of them apply until you trust them."}
      </div>
      {/* Last trusted → now; from /dev/null when nothing was trusted yet. */}
      <UnifiedDiff diff={status.diff} testId="project-trust-diff" style={{ fontSize: 11, margin: "0 0 8px", maxHeight: 260 }} />
      <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
        <button
          type="button"
          data-testid="project-trust-button"
          onClick={handleTrust}
          disabled={busy}
          style={{
            padding: "5px 10px",
            backgroundColor: "transparent",
            border: `1px solid ${colors.warning}`,
            borderRadius: 5,
            color: colors.text,
            fontSize: 11,
            cursor: busy ? "default" : "pointer",
            opacity: busy ? 0.6 : 1,
            fontFamily: "inherit",
          }}
        >
          Trust this config
        </button>
        {message && <span style={{ color: colors.error }}>{message}</span>}
      </div>
    </div>
  );
}
