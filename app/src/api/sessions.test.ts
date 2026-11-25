import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { fetchSessions, setSession } from "./sessions";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("sessions", () => {
  it("fetches the sessions", async () => {
    fetchMock.mockResolvedValue(json({ current_session_id: "s", sessions: [], imported_session_ids: [] }));
    await expect(fetchSessions("c1")).resolves.toEqual({ current_session_id: "s", sessions: [], imported_session_ids: [] });
    expect(sent(fetchMock).url).toBe(`${API}/api/channels/c1/sessions`);
  });

  it("throws when fetching fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchSessions("c1")).rejects.toThrow("Failed to fetch sessions: boom");
  });

  it("sets the session", async () => {
    fetchMock.mockResolvedValue(json({ deferred: true }));
    await expect(setSession("c1", "s2")).resolves.toEqual({ deferred: true });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/channels/c1/session`, method: "PUT", body: { session_id: "s2" } });
  });

  it("throws the server text or the status text when setting fails", async () => {
    fetchMock.mockResolvedValueOnce(fail(404, "Not Found", "unknown session\n"));
    await expect(setSession("c1", "s")).rejects.toThrow(/^unknown session$/);
    fetchMock.mockResolvedValueOnce(fail(404, "Not Found"));
    await expect(setSession("c1", "s")).rejects.toThrow("Not Found");
  });
});
