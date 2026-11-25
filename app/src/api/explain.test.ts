import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch, unreadable } from "../test/hooks-c-api-fetch";
import { explainTurn, fetchExplainState, fetchExplanations, setExplain } from "./explain";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchExplainState", () => {
  it("returns the state", async () => {
    fetchMock.mockResolvedValue(json({ available: true, explain: "", default_explain: true, enabled: true }));
    await expect(fetchExplainState("c1")).resolves.toEqual({ available: true, explain: "", default_explain: true, enabled: true });
    expect(sent(fetchMock).url).toBe(`${API}/api/channels/c1/explain`);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchExplainState("c1")).rejects.toThrow("Failed to fetch explain state: boom");
  });
});

describe("setExplain", () => {
  it("puts the setting", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await setExplain("c1", "on");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/channels/c1/explain`, method: "PUT", body: { explain: "on" } });
    expect(sent(fetchMock).headers.get("Content-Type")).toBe("application/json");
  });

  it("throws the server's message, or the status text when there is none or it can't be read", async () => {
    fetchMock.mockResolvedValueOnce(fail(400, "Bad Request", " task thread \n"));
    await expect(setExplain("c1", "on")).rejects.toThrow(/^task thread$/);
    fetchMock.mockResolvedValueOnce(fail(400, "Bad Request"));
    await expect(setExplain("c1", "on")).rejects.toThrow("Failed to set explain: Bad Request");
    fetchMock.mockResolvedValueOnce(unreadable(500, "boom"));
    await expect(setExplain("c1", "on")).rejects.toThrow("Failed to set explain: boom");
  });
});

describe("fetchExplanations", () => {
  it("returns the explanations", async () => {
    fetchMock.mockResolvedValue(json([{ id: 1, status: "done" }]));
    await expect(fetchExplanations("c1")).resolves.toEqual([{ id: 1, status: "done" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/channels/c1/explanations`);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchExplanations("c1")).rejects.toThrow("Failed to fetch explanations: boom");
  });
});

describe("explainTurn", () => {
  it("posts the message id, not forced by default", async () => {
    fetchMock.mockResolvedValue(json({ id: 2, status: "queued" }));
    await expect(explainTurn("c1", "m1")).resolves.toEqual({ id: 2, status: "queued" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/channels/c1/explanations`, method: "POST", body: { message_id: "m1", force: false } });
  });

  it("forces a re-explain", async () => {
    fetchMock.mockResolvedValue(json({ id: 3 }));
    await explainTurn("c1", "m1", true);
    expect(sent(fetchMock).body).toEqual({ message_id: "m1", force: true });
  });

  it("throws the server's message or the status text", async () => {
    fetchMock.mockResolvedValueOnce(fail(409, "Conflict", "explain is off"));
    await expect(explainTurn("c1", "m1")).rejects.toThrow("explain is off");
    fetchMock.mockResolvedValueOnce(fail(409, "Conflict"));
    await expect(explainTurn("c1", "m1")).rejects.toThrow("Failed to explain: Conflict");
  });
});
