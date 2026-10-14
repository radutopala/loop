import { useCallback, useEffect, useRef, useState } from "react";
import {
  type ConfigRevision,
  type ConfigRevisionSummary,
  configRevisionSourceLabel,
  fetchConfigHistory,
  fetchConfigHistoryFiles,
  fetchConfigRevision,
  restoreConfigRevision,
} from "../../api/configApi";
import type { ColorPalette } from "../../theme";
import { logErr } from "../../utils/log";
import { UnifiedDiff } from "./UnifiedDiff";

const CONFIG_FILE = "config.json";

// ConfigHistory lists the revisions Loop recorded of the global config, or
// of the project config when channelId is set, or of a file of that
// .loop dir, shows what each changed or how it differs from another
// revision, and restores one.
export function ConfigHistory({
  channelId,
  colors,
  refreshKey,
  dirty,
  onRestored,
}: {
  channelId?: string;
  colors: ColorPalette;
  /** Changes when the config was saved, to list the new revision. */
  refreshKey: unknown;
  /** The config has unsaved edits, which a restore of it would lose. */
  dirty: boolean;
  /** Called after a restore of the config, to re-fetch it. */
  onRestored: () => void;
}) {
  // The file shown, relative to the .loop dir; back to the config when the project changes.
  const [file, setFile] = useState(CONFIG_FILE);
  const [fileChannel, setFileChannel] = useState(channelId);
  if (fileChannel !== channelId) {
    setFileChannel(channelId);
    setFile(CONFIG_FILE);
  }
  // The other recorded files of the .loop dir, to pick from.
  const [files, setFiles] = useState<string[]>([]);
  const [revisions, setRevisions] = useState<ConfigRevisionSummary[] | null>(null);
  const [path, setPath] = useState("");
  const [missing, setMissing] = useState(false);
  const [selected, setSelected] = useState<ConfigRevision | null>(null);
  // The revision the selected one is diffed against; null is the one before it.
  const [against, setAgainst] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  // Counts revision fetches, so a slower earlier one doesn't overwrite a later one.
  const fetchSeq = useRef(0);
  // The same for history fetches, which a file change starts.
  const loadSeq = useRef(0);

  const load = useCallback(() => {
    const seq = ++loadSeq.current;
    fetchConfigHistory(channelId, file === CONFIG_FILE ? undefined : file)
      .then((h) => {
        if (seq !== loadSeq.current) return;
        setPath(h.path);
        setMissing(!!h.missing);
        setRevisions(h.revisions);
      })
      .catch((e) => {
        if (seq === loadSeq.current) setRevisions([]);
        logErr("fetching config history")(e);
      });
    fetchConfigHistoryFiles(channelId)
      .then((f) => {
        if (seq === loadSeq.current) setFiles(f.files.map((x) => x.file).filter((x) => x !== CONFIG_FILE));
      })
      .catch(logErr("fetching config history files"));
  }, [channelId, file]);

  useEffect(() => {
    fetchSeq.current++;
    setSelected(null);
    setAgainst(null);
    setMessage(null);
    load();
  }, [load, refreshKey]);

  const select = (id: number, other: number | null) => {
    setMessage(null);
    setAgainst(other);
    const seq = ++fetchSeq.current;
    fetchConfigRevision(id, other ?? undefined)
      .then((rev) => {
        if (seq === fetchSeq.current) setSelected(rev);
      })
      .catch((e) => {
        if (seq === fetchSeq.current) setMessage(e.message ?? "Failed to load the revision");
      });
  };

  const handleRestore = async () => {
    if (!selected) return;
    setBusy(true);
    setMessage(null);
    try {
      await restoreConfigRevision(selected.id);
      fetchSeq.current++;
      setSelected(null);
      setAgainst(null);
      load();
      if (file === CONFIG_FILE) onRestored();
    } catch (e: any) {
      setMessage(e.message ?? "Failed to restore");
    } finally {
      setBusy(false);
    }
  };

  // The newest revision, unless the file is gone and it needs restoring.
  const current = missing ? undefined : revisions?.[0]?.id;
  // Only a restore of the config loses its unsaved edits.
  const unsaved = dirty && file === CONFIG_FILE;
  // What the shown diff goes from: the against revision, or the one before
  // the selected one (none for the oldest).
  const selectedIndex = revisions?.findIndex((r) => r.id === selected?.id) ?? -1;
  const fromId = against ?? revisions?.[selectedIndex + 1]?.id;
  const revisionLabel = (r: ConfigRevisionSummary) => `#${r.id} · ${configRevisionSourceLabel(r.source)}${r.id === current ? " (current)" : ""} · ${new Date(r.created_at).toLocaleString()}`;
  const selectStyle: React.CSSProperties = {
    backgroundColor: colors.bg,
    border: `1px solid ${colors.border}`,
    borderRadius: 6,
    padding: "4px 8px",
    fontSize: 11,
    fontFamily: "inherit",
    color: colors.text,
    outline: "none",
    cursor: "pointer",
    minWidth: 0,
    maxWidth: 360,
  };
  const btnStyle = (enabled: boolean): React.CSSProperties => ({
    padding: "5px 10px",
    backgroundColor: "transparent",
    border: `1px solid ${colors.border}`,
    borderRadius: 5,
    color: colors.text,
    fontSize: 11,
    cursor: enabled ? "pointer" : "default",
    opacity: enabled ? 1 : 0.6,
    fontFamily: "inherit",
  });

  return (
    <div data-testid="config-history" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1, fontSize: 12, color: colors.text }}>
      <div style={{ display: "flex", gap: 12, alignItems: "center", justifyContent: "space-between", marginBottom: 4 }}>
        <div style={{ fontSize: 14, fontWeight: 600 }}>{channelId ? "Project Config History" : "Global Config History"}</div>
        <label style={{ display: "flex", gap: 6, alignItems: "center", color: colors.textMuted, minWidth: 0 }}>
          File
          <select
            data-testid="config-history-file"
            value={file}
            onChange={(e) => {
              setRevisions(null);
              setPath("");
              setMissing(false);
              setFile(e.target.value);
            }}
            style={selectStyle}
          >
            {[CONFIG_FILE, ...files].map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div style={{ color: colors.textMuted, marginBottom: 12 }}>
        Each change to <code>{path || file}</code>
        {missing && (
          <span data-testid="config-history-missing" style={{ color: colors.error }}>
            {" "}
            (deleted)
          </span>
        )}
        , including edits made outside Loop. The newest 200 are kept.
      </div>
      {revisions === null ? (
        <div style={{ color: colors.textDim }}>Loading...</div>
      ) : revisions.length === 0 ? (
        <div style={{ color: colors.textDim }}>No history yet.</div>
      ) : (
        <div style={{ display: "flex", gap: 12, minHeight: 0, flex: 1 }}>
          <div style={{ width: 240, flexShrink: 0, overflow: "auto", border: `1px solid ${colors.border}`, borderRadius: 6 }}>
            {revisions.map((r) => (
              <button
                key={r.id}
                type="button"
                data-testid="config-history-item"
                onClick={() => select(r.id, against === r.id ? null : against)}
                style={{
                  display: "block",
                  width: "100%",
                  textAlign: "left",
                  padding: "6px 10px",
                  border: "none",
                  borderBottom: `1px solid ${colors.border}`,
                  backgroundColor: selected?.id === r.id ? colors.hoverBg : "transparent",
                  color: colors.text,
                  cursor: "pointer",
                  fontFamily: "inherit",
                  fontSize: 12,
                }}
              >
                <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
                  <span>
                    {configRevisionSourceLabel(r.source)}
                    {r.id === current && <span style={{ color: colors.textDim }}> · current</span>}
                  </span>
                  <span style={{ whiteSpace: "nowrap" }}>
                    <span style={{ color: colors.diffAddText }}>+{r.added}</span> <span style={{ color: colors.diffDelText }}>−{r.removed}</span>
                  </span>
                </div>
                <div style={{ color: colors.textDim, fontSize: 11 }}>
                  #{r.id} · {new Date(r.created_at).toLocaleString()}
                </div>
              </button>
            ))}
          </div>
          <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", minHeight: 0 }}>
            {selected ? (
              <>
                <div style={{ display: "flex", gap: 10, alignItems: "center", marginBottom: 8 }}>
                  <button
                    type="button"
                    data-testid="config-history-restore"
                    onClick={handleRestore}
                    disabled={busy || unsaved || selected.id === current}
                    title={unsaved ? "Save or cancel your edits first" : selected.id === current ? "This is the current version" : undefined}
                    style={btnStyle(!busy && !unsaved && selected.id !== current)}
                  >
                    Restore this version
                  </button>
                  <label style={{ display: "flex", gap: 6, alignItems: "center", color: colors.textMuted, minWidth: 0 }}>
                    Diff against
                    <select data-testid="config-history-against" value={against ?? ""} onChange={(e) => select(selected.id, e.target.value === "" ? null : Number(e.target.value))} style={selectStyle}>
                      <option value="">The revision before it</option>
                      {revisions
                        .filter((r) => r.id !== selected.id)
                        .map((r) => (
                          <option key={r.id} value={r.id}>
                            {revisionLabel(r)}
                          </option>
                        ))}
                    </select>
                  </label>
                  {message && <span style={{ color: colors.error }}>{message}</span>}
                </div>
                <div data-testid="config-history-diff-caption" style={{ color: colors.textDim, fontSize: 11, marginBottom: 6 }}>
                  {fromId === undefined ? `#${selected.id}, the oldest revision, in full` : `Changes from #${fromId} to #${selected.id}`}
                </div>
                <UnifiedDiff diff={selected.diff} testId="config-history-diff" style={{ fontSize: 11, margin: 0, flex: 1, minHeight: 0, maxHeight: "none", overflow: "auto" }} />
              </>
            ) : (
              <div style={{ color: message ? colors.error : colors.textDim }}>{message ?? "Select a revision to see what it changed."}</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
