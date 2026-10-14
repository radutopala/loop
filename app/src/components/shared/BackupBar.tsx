import { useCallback, useEffect, useState } from "react";
import { type BackupStatus, createBackupKey, fetchBackupStatus, startBackup } from "../../api/backup";
import type { ColorPalette } from "../../theme";
import { writeClipboard } from "../../utils/copyToClipboard";
import { formatBytes } from "../../utils/formatBytes";
import { logErr } from "../../utils/log";

// How often the bar re-reads the status while a backup is being written.
const POLL_MS = 2000;

// backupSummary describes the newest backup, all of them and the next
// scheduled one.
export function backupSummary(st: BackupStatus, fmtTime: (iso: string) => string = (iso) => new Date(iso).toLocaleString()): string {
  const parts: string[] = [];
  if (st.running) parts.push("Backing up…");
  if (st.last) parts.push(`Last backup ${fmtTime(st.last.at)} (${formatBytes(st.last.size)}), ${st.count} kept in ${st.dir} (${formatBytes(st.total_size)} in all)`);
  else if (st.dir) parts.push(`No backups in ${st.dir} yet`);
  else parts.push("Set a backup directory to back up the database");
  if (!st.enabled) parts.push("Scheduled backups are off");
  else if (st.next_at && !st.running) parts.push(`next ${fmtTime(st.next_at)}`);
  return parts.join(" · ");
}

// shortKey shortens a public key to its start and end.
export function shortKey(key: string): string {
  return key.length > 24 ? `${key.slice(0, 12)}…${key.slice(-8)}` : key;
}

// encryptionNote says whether new backups are encrypted.
export function encryptionNote(st: BackupStatus): string {
  if (st.recipient) return `Encrypted to the public key ${shortKey(st.recipient)}`;
  return "Not encrypted: whoever can read the backup directory can read your chats and the tokens in your config. Create a key to encrypt new backups.";
}

// BackupBar shows how the backups went, backs up on demand and creates the
// key they're encrypted with; refreshKey re-reads the status after the
// config is saved. A key can't be created while the form has unsaved
// edits: saving them would drop the public key it sets. onKeyCreated
// re-reads the config, which now holds it.
export function BackupBar({ colors, refreshKey, dirty, onKeyCreated }: { colors: ColorPalette; refreshKey: unknown; dirty: boolean; onKeyCreated: () => void }) {
  const [status, setStatus] = useState<BackupStatus | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  // privateKey is shown once, right after it's created.
  const [privateKey, setPrivateKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const load = useCallback(() => {
    fetchBackupStatus().then(setStatus).catch(logErr("fetching backup status"));
  }, []);

  useEffect(load, [load, refreshKey]);

  const running = status?.running ?? false;
  useEffect(() => {
    if (!running) return;
    const id = setInterval(load, POLL_MS);
    return () => clearInterval(id);
  }, [running, load]);

  const handleStart = () => {
    setStartError(null);
    startBackup()
      .then(load)
      .catch((e: Error) => setStartError(e.message));
  };

  const handleCreateKey = () => {
    if (status?.recipient && !window.confirm("Create a new key? Backups written so far stay encrypted to the current key; keep its private key to restore them.")) return;
    setStartError(null);
    createBackupKey()
      .then((key) => {
        setPrivateKey(key.identity);
        setCopied(false);
        onKeyCreated();
        load();
      })
      .catch((e: Error) => setStartError(e.message));
  };

  const handleCopy = () => {
    if (!privateKey) return;
    writeClipboard(privateKey)
      .then(() => setCopied(true))
      .catch(logErr("copying the private key"));
  };

  if (!status) return null;
  const error = startError ?? status.error;
  const buttonStyle = (disabled: boolean) => ({
    padding: "5px 10px",
    backgroundColor: "transparent",
    border: `1px solid ${colors.border}`,
    borderRadius: 5,
    color: colors.text,
    fontSize: 11,
    cursor: disabled ? "default" : "pointer",
    opacity: disabled ? 0.6 : 1,
    fontFamily: "inherit",
    flexShrink: 0,
  });
  return (
    <div
      data-testid="backup-bar"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 6,
        marginBottom: 12,
        padding: "8px 10px",
        backgroundColor: colors.bg,
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
      }}
    >
      <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
        <button data-testid="backup-now" onClick={handleStart} disabled={running || !status.dir} style={buttonStyle(running || !status.dir)}>
          {running ? "Backing up…" : "Back up now"}
        </button>
        <span data-testid="backup-summary" style={{ fontSize: 11, color: colors.textDim, lineHeight: 1.4 }}>
          {backupSummary(status)}
        </span>
      </div>
      <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
        <button data-testid="backup-create-key" onClick={handleCreateKey} disabled={dirty} title={dirty ? "Save your changes first" : undefined} style={buttonStyle(dirty)}>
          {status.recipient ? "Create a new key" : "Create key"}
        </button>
        <span data-testid="backup-encryption" style={{ fontSize: 11, color: status.recipient ? colors.textDim : colors.warning, lineHeight: 1.4 }}>
          {encryptionNote(status)}
        </span>
      </div>
      {privateKey && (
        <div data-testid="backup-private-key" style={{ display: "flex", flexDirection: "column", gap: 6, padding: "8px 10px", border: `1px solid ${colors.warning}`, borderRadius: 5 }}>
          <span style={{ fontSize: 11, color: colors.text, lineHeight: 1.4 }}>
            Save this private key in your password manager now. Loop doesn't keep it, and encrypted backups can't be restored without it.
          </span>
          <code data-testid="backup-private-key-value" style={{ fontSize: 11, color: colors.text, wordBreak: "break-all", userSelect: "all" }}>
            {privateKey}
          </code>
          <div style={{ display: "flex", gap: 8 }}>
            <button data-testid="backup-copy-key" onClick={handleCopy} style={buttonStyle(false)}>
              {copied ? "Copied" : "Copy"}
            </button>
            <button data-testid="backup-hide-key" onClick={() => setPrivateKey(null)} style={buttonStyle(false)}>
              I saved it
            </button>
          </div>
        </div>
      )}
      {error && (
        <span data-testid="backup-error" style={{ fontSize: 11, color: colors.error, lineHeight: 1.4 }}>
          {status.error_at && !startError ? `Failed ${new Date(status.error_at).toLocaleString()}: ` : ""}
          {error}
        </span>
      )}
    </div>
  );
}
