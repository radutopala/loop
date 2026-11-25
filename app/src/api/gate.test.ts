import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch, unreadable } from "../test/hooks-c-api-fetch";
import { GateApprovalGoneError, listPendingApprovals, resolveGateApproval } from "./gate";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("listPendingApprovals", () => {
  it("returns the approvals", async () => {
    fetchMock.mockResolvedValue(json({ approvals: [{ req_id: "r1", container_id: "x", channel_id: "c1" }] }));
    await expect(listPendingApprovals()).resolves.toEqual([{ req_id: "r1", container_id: "x", channel_id: "c1" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/gate/approvals`);
  });

  it("returns none when the body has no list", async () => {
    fetchMock.mockResolvedValue(json({}));
    await expect(listPendingApprovals()).resolves.toEqual([]);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(503, "Unavailable"));
    await expect(listPendingApprovals()).rejects.toThrow("Failed to list gate approvals: Unavailable");
  });
});

describe("resolveGateApproval", () => {
  it("posts the decision", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await resolveGateApproval("r/1", "deny-session");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/gate/approvals/r%2F1`, method: "POST", body: { decision: "deny-session" } });
  });

  it("throws GateApprovalGoneError on a 404", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    const err = await resolveGateApproval("r1", "once").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(GateApprovalGoneError);
    expect(err).toMatchObject({ name: "GateApprovalGoneError", message: "approval request no longer exists" });
  });

  it("throws the server's message, or the status text when there is none or it can't be read", async () => {
    fetchMock.mockResolvedValueOnce(fail(500, "boom", "gate down"));
    await expect(resolveGateApproval("r1", "once")).rejects.toThrow("Failed to resolve gate approval: gate down");
    fetchMock.mockResolvedValueOnce(fail(500, "boom"));
    await expect(resolveGateApproval("r1", "once")).rejects.toThrow("Failed to resolve gate approval: boom");
    fetchMock.mockResolvedValueOnce(unreadable(502, "Bad Gateway"));
    await expect(resolveGateApproval("r1", "once")).rejects.toThrow("Failed to resolve gate approval: Bad Gateway");
  });
});
