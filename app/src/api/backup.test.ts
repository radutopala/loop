import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createBackupKey, fetchBackupStatus, startBackup } from "./backup";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchBackupStatus", () => {
  it("returns the backup status", async () => {
    const status = { enabled: true, dir: "/backups", times_per_day: 2, keep_last: 3, keep_daily: 7, keep_weekly: 4, keep_monthly: 6, running: false, count: 1, total_size: 10 };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(status)));
    await expect(fetchBackupStatus()).resolves.toEqual(status);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/backup");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 501, statusText: "boom" }));
    await expect(fetchBackupStatus()).rejects.toThrow("Failed to fetch backup status: boom");
  });
});

describe("startBackup", () => {
  it("posts to start a backup", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 202 }));
    await expect(startBackup()).resolves.toBeUndefined();
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/backup");
    expect(fetchMock.mock.calls[0]![1].method).toBe("POST");
  });

  it("throws the server's message", async () => {
    fetchMock.mockResolvedValue(new Response("no backup directory is configured\n", { status: 400 }));
    await expect(startBackup()).rejects.toThrow("no backup directory is configured");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 409, statusText: "Conflict" }));
    await expect(startBackup()).rejects.toThrow("Conflict");
  });
});

describe("createBackupKey", () => {
  it("posts to create a key and returns both keys", async () => {
    const key = { recipient: "age1pub", identity: "AGE-SECRET-KEY-1PRIV" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(key)));
    await expect(createBackupKey()).resolves.toEqual(key);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/backup/key");
    expect(fetchMock.mock.calls[0]![1].method).toBe("POST");
  });

  it("throws the server's message", async () => {
    fetchMock.mockResolvedValue(new Response("saving the public key: denied\n", { status: 500 }));
    await expect(createBackupKey()).rejects.toThrow("saving the public key: denied");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(createBackupKey()).rejects.toThrow("Internal Server Error");
  });
});
