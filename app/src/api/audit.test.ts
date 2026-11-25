import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { deleteAuditFile, fetchAuditFiles } from "./audit";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("audit", () => {
  it("lists a page of audit files", async () => {
    fetchMock.mockResolvedValue(json({ files: [{ date: "2026-01-02", size: 3, last_modified: "x" }], total: 1 }));
    await expect(fetchAuditFiles("c1", 10, 20)).resolves.toEqual({ files: [{ date: "2026-01-02", size: 3, last_modified: "x" }], total: 1 });
    expect(sent(fetchMock).url).toBe(`${API}/api/channels/c1/audit?offset=10&limit=20`);
  });

  it("throws when listing fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchAuditFiles("c1", 0, 1)).rejects.toThrow("Failed to fetch audit files: boom");
  });

  it("deletes one day's file", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await deleteAuditFile("c1", "2026/01");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/channels/c1/audit/2026%2F01`, method: "DELETE" });
  });

  it("throws when the delete fails", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    await expect(deleteAuditFile("c1", "d")).rejects.toThrow("Failed to delete audit file: Not Found");
  });
});
