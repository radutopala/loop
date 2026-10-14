import { describe, expect, it } from "vitest";
import type { BackupStatus } from "../../api/backup";
import { backupSummary, encryptionNote, shortKey } from "./BackupBar";

const base: BackupStatus = {
  enabled: true,
  dir: "/backups",
  times_per_day: 2,
  keep_last: 3,
  keep_daily: 7,
  keep_weekly: 4,
  keep_monthly: 6,
  running: false,
  count: 0,
  total_size: 0,
};
const fmt = (iso: string) => `<${iso}>`;

describe("backupSummary", () => {
  it.each<[string, Partial<BackupStatus>, string]>([
    ["asks for a directory", { enabled: false, dir: undefined }, "Set a backup directory to back up the database · Scheduled backups are off"],
    ["says there's none yet", { next_at: "N" }, "No backups in /backups yet · next <N>"],
    [
      "describes the newest backup and the next",
      { last: { file: "f", path: "p", at: "A", size: 2048, encrypted: true }, count: 3, total_size: 6144, next_at: "N" },
      "Last backup <A> (2.0 KB), 3 kept in /backups (6.0 KB in all) · next <N>",
    ],
    [
      "says scheduled backups are off",
      { enabled: false, last: { file: "f", path: "p", at: "A", size: 1, encrypted: false }, count: 1, total_size: 1 },
      "Last backup <A> (1 B), 1 kept in /backups (1 B in all) · Scheduled backups are off",
    ],
    ["hides the next run while backing up", { running: true, next_at: "N" }, "Backing up… · No backups in /backups yet"],
  ])("%s", (_, patch, want) => {
    expect(backupSummary({ ...base, ...patch }, fmt)).toBe(want);
  });

  it("formats times as local time by default", () => {
    const at = "2026-10-14T08:00:00Z";
    expect(backupSummary({ ...base, last: { file: "f", path: "p", at, size: 1, encrypted: false }, count: 1 })).toContain(new Date(at).toLocaleString());
  });
});

describe("shortKey", () => {
  it.each([
    ["age1short", "age1short"],
    ["age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqzzzzzzzz", "age1qqqqqqqq…zzzzzzzz"],
  ])("%s", (key, want) => {
    expect(shortKey(key)).toBe(want);
  });
});

describe("encryptionNote", () => {
  it("names the key backups are encrypted to", () => {
    expect(encryptionNote({ ...base, recipient: "age1short" })).toBe("Encrypted to the public key age1short");
  });

  it("warns that backups aren't encrypted", () => {
    expect(encryptionNote(base)).toMatch(/^Not encrypted: .*Create a key/);
  });
});
