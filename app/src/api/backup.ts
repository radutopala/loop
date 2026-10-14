import { apiFetch, getApiUrl } from "./api";

export interface BackupFile {
  file: string;
  path: string;
  at: string;
  size: number;
  encrypted: boolean;
}

export interface BackupStatus {
  enabled: boolean;
  dir?: string;
  times_per_day: number;
  keep_last: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  // recipient is the public key new backups are encrypted to; unset
  // writes them unencrypted.
  recipient?: string;
  running: boolean;
  last?: BackupFile;
  count: number;
  total_size: number;
  next_at?: string;
  error?: string;
  error_at?: string;
}

export async function fetchBackupStatus(): Promise<BackupStatus> {
  const res = await apiFetch(`${getApiUrl()}/api/backup`);
  if (!res.ok) throw new Error(`Failed to fetch backup status: ${res.statusText}`);
  return res.json();
}

// startBackup starts a backup now; fetchBackupStatus tells when it's done.
export async function startBackup(): Promise<void> {
  const res = await apiFetch(`${getApiUrl()}/api/backup`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
}

export interface BackupKey {
  // recipient is the public key, now the global config's backup.recipient.
  recipient: string;
  // identity is the private key. Loop keeps no copy: this is the only time
  // it's shown.
  identity: string;
}

// createBackupKey generates the key pair backups are encrypted with and
// sets its public key in the global config.
export async function createBackupKey(): Promise<BackupKey> {
  const res = await apiFetch(`${getApiUrl()}/api/backup/key`, { method: "POST" });
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return res.json();
}
