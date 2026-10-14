---
title: Backup
---
Loop can back up its database (`~/.loop/loop.db`) and playgrounds to a directory on a schedule. Point it at a folder a sync client uploads, such as Dropbox, OneDrive, Google Drive or iCloud Drive, and the backups leave the machine. Backups can be encrypted to a key only you hold.

**Package:** `internal/backup`

## Configuration

Set it in **Settings → Global → Backup**, or in `~/.loop/config.json`. It's global only; a project config can't set it.

```json
"backup": {
  "enabled": true,
  "path": "~/Dropbox/loop-backups",
  "times_per_day": 2,
  "keep_last": 3,
  "keep_daily": 7,
  "keep_weekly": 4,
  "keep_monthly": 6,
  "recipient": "age1..."
}
```

| Field | Default | Description |
|---|---|---|
| `enabled` | `false` | Back up on the schedule. **Back up now** works without it. |
| `path` | — | The directory to write backups to; created if missing. A leading `~/` is the home directory; otherwise the path must be absolute. |
| `times_per_day` | `1` | How many backups a day, evenly spaced: `2` is every 12 hours. At most `24`. |
| `keep_last` | `3` | Keep the newest this many backups. |
| `keep_daily` | `7` | Keep the newest backup of each of the last this many days that have one. |
| `keep_weekly` | `4` | The same, per ISO week. |
| `keep_monthly` | `6` | The same, per month. |
| `recipient` | — | The [age](https://age-encryption.org) public key (`age1...`) to encrypt backups to. Unset, backups aren't encrypted. |

Days, weeks and months are counted in UTC. `0` turns a retention tier off. A backup kept by any tier stays, and the newest backup is always kept. With the defaults and one backup a day, 14 backups are kept, the oldest four to five months old.

Changes apply within a minute, without restarting the daemon.

## What's in a backup

A backup is a zip:

| Entry | Content |
|---|---|
| `loop.db` | The database: chats, tasks, workflows, memory, config history |
| `playground/global/` | `~/.loop/playground` |
| `playground/projects/<name>/` | A project's `.loop/playground`, in a folder named after the project's dir (`-2`, `-3` are appended when two share a name) |
| `manifest.json` | When the backup was written, by which Loop version, and which dir each playground folder came from |

The config files aren't backed up as files: their revisions are in the database's [config history](settings.md#config-history), which also records the files they point at and the agent container's build files.

The database holds the tokens in your config, through config history. **Unencrypted backups can be read by anyone who can read the backup directory**, including the cloud provider. Set a `recipient` to encrypt them.

## Encryption

Backups are encrypted with [age](https://age-encryption.org) to an X25519 public key. Only the matching private key can decrypt them; Loop never stores it.

To create a key, click **Create key** in **Settings → Backup**. Loop generates a key pair, saves the public key as `recipient` and shows the private key (`AGE-SECRET-KEY-1...`) once. Copy it into a password manager. Without it, encrypted backups can't be restored. To use a key of your own, run `age-keygen -o key.txt` and set `recipient` to the public key it prints.

Encrypted backups are named `.zip.age`. Creating a new key encrypts only new backups to it; older backups still need the old private key.

## How it works

- Every minute the daemon checks whether a backup is due: `24h / times_per_day` after the newest backup in the directory. The schedule comes from the files themselves, so it carries over daemon restarts, and the first backup is written as soon as backups are enabled.
- The database snapshot is taken with SQLite's `VACUUM INTO`, a consistent copy that doesn't block the daemon's writes. It's written next to the database, zipped with the playgrounds into the backup directory, and deleted. Playground files that can't be read are left out and logged.
- The backup is written as a hidden `.loop-*.partial` file and renamed when complete, so a sync client never uploads half a backup under the final name. Encryption is streamed into the same file; no unencrypted copy is written to the backup directory.
- Backups are named `loop-<UTC time>.zip` or `.zip.age`, e.g. `loop-20261014T080000Z.zip.age`. Each backup gets a new name and none is ever overwritten. If a file of that name exists, the backup fails rather than replace it. Only files named that way are counted and pruned; anything else in the directory is left alone. Partial files left by a backup that didn't finish are deleted with the next one.
- A failed backup is retried 15 minutes later. The failure shows in the Backup section of Settings until a backup succeeds.

Sync clients such as Dropbox keep deleted files and old versions for a limited time, often 30 days. Because every backup has its own name and isn't modified afterwards, the retention tiers decide how far back backups go, not the provider's version history. A pruned backup goes to the provider's trash like any other deleted file.

## Settings

The **Backup** section shows the newest backup, its time and size, how many are kept and their total size, when the next one is due and the last failure. **Back up now** writes a backup right away, whether or not `enabled` is set, as long as `path` is. A note says whether new backups are encrypted, next to **Create key**. Backed by [`GET /api/backup`](api.md#get-apibackup), [`POST /api/backup`](api.md#post-apibackup) and [`POST /api/backup/key`](api.md#post-apibackupkey).

## Restoring

```bash
loop backup:restore ~/Dropbox/loop-backups/loop-20261014T080000Z.zip.age
```

- For an encrypted backup, paste the private key when asked, or pass the file it's saved in with `--identity key.txt`. The backup is decrypted in memory, so no unencrypted copy is written.
- The database is checked before anything is replaced. The daemon is stopped, the current database (with its `-wal` and `-shm` files) is moved aside as `loop.db.before-restore-<time>` (numbered when that name is taken), and the backup's database takes its place. The daemon is then started again if it was running.
- A playground is put back only when no playground of that name exists. Existing playgrounds are left as they are, and listed. Project playgrounds go back to the project dirs named in the manifest.
- If you run `loop serve` in a terminal rather than as the daemon, stop it before restoring.
- The command reads `~/.loop/config.json` to find the database. On a new machine, run `loop onboard:global` first. Your configs' revisions are in the restored database: restore them from **Settings → History**.

### Without Loop

A backup is a plain zip, and age is an open format with [several implementations](https://age-encryption.org). With the `age` CLI:

```bash
age -d -i key.txt -o backup.zip loop-20261014T080000Z.zip.age
unzip backup.zip -d backup
```

`key.txt` holds the private key. Then stop the daemon, move `~/.loop/loop.db` and its `-wal` and `-shm` files aside, copy `backup/loop.db` in its place, and start the daemon.
