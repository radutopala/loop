import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  approveReview,
  deleteReviewComment,
  deleteReviewSession,
  getReviewSession,
  getReviewStats,
  listReviewPRs,
  listReviewSessions,
  loadReviewPR,
  pushAllReviewComments,
  pushReviewComment,
  runReview,
  setReviewAgent,
  setReviewFork,
  setReviewScope,
  startReview,
  stopReview,
  syncReviewSession,
} from "./review";

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

describe("setReviewScope", () => {
  it("PUTs the scope and returns the session with its comments normalized", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ present: true, session: { channel_id: "ch1", comments: null, status: "ready", scope: "incremental" } })));
    const resp = await setReviewScope("ch1", "incremental");
    expect(resp.session?.scope).toBe("incremental");
    expect(resp.session?.comments).toEqual([]);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/ch1/review/scope");
    expect(init.method).toBe("PUT");
    expect(init.body).toBe('{"scope":"incremental"}');
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("no review session for channel", { status: 404 }));
    await expect(setReviewScope("ch1", "")).rejects.toThrow("no review session for channel");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 400, statusText: "Bad Request" }));
    await expect(setReviewScope("ch1", "full")).rejects.toThrow("Failed to set review scope: Bad Request");
  });
});

describe("startReview", () => {
  const req = { pr_number: 7, mode: "review" as const, max_iterations: 3, model: "", effort: "high" };

  it("POSTs the start with the config's defaults and returns the run and session", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ run_id: "run-1", session: { channel_id: "ch1", comments: null, status: "ready", review_id: "rev-2" } }), { status: 201 }));
    const resp = await startReview("ch1", req);
    expect(resp.run_id).toBe("run-1");
    expect(resp.session?.review_id).toBe("rev-2");
    expect(resp.session?.comments).toEqual([]);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/ch1/review/start");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ ...req, config_defaults: true });
  });

  it("returns no session when the daemon sends none", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ run_id: "run-1", session: null }), { status: 201 }));
    expect(await startReview("ch1", req)).toEqual({ run_id: "run-1", session: undefined });
  });

  it("throws the daemon's message, or the status text", async () => {
    fetchMock.mockResolvedValueOnce(new Response("a review is starting for this channel", { status: 409 }));
    await expect(startReview("ch1", req)).rejects.toThrow("a review is starting for this channel");
    fetchMock.mockResolvedValueOnce(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(startReview("ch1", req)).rejects.toThrow("Failed to start review: Internal Server Error");
  });
});

describe("getReviewStats", () => {
  it.each([
    [{}, "http://localhost:8222/api/channels/ch1/review/stats"],
    [{ days: 30 }, "http://localhost:8222/api/channels/ch1/review/stats?days=30"],
    [{ days: 30, pr: 7 }, "http://localhost:8222/api/channels/ch1/review/stats?days=30&pr=7"],
  ])("GETs %o", async (opts, want) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ runs: { total: 1 } })));
    await expect(getReviewStats("ch1", opts)).resolves.toMatchObject({ runs: { total: 1 } });
    expect(fetchMock.mock.calls[0]![0]).toBe(want);
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("review history not configured", { status: 501 }));
    await expect(getReviewStats("ch1")).rejects.toThrow("review history not configured");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(getReviewStats("ch1")).rejects.toThrow("Failed to load review stats: Internal Server Error");
  });
});

describe("getReviewSession", () => {
  it.each([
    [{}, "http://localhost:8222/api/channels/ch1/review"],
    [{ diff: true }, "http://localhost:8222/api/channels/ch1/review"],
    [{ diff: false }, "http://localhost:8222/api/channels/ch1/review?diff=false"],
  ])("GETs %o", async (opts, want) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ present: true, session: { channel_id: "ch1", comments: null, status: "ready" }, outcome: { state: "approve" } })));
    const resp = await getReviewSession("ch1", opts);
    expect(resp.session?.comments).toEqual([]);
    expect(resp.outcome?.state).toBe("approve");
    expect(fetchMock.mock.calls[0]![0]).toBe(want);
  });

  it("throws with the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(getReviewSession("ch1")).rejects.toThrow("Failed to fetch review session: Internal Server Error");
  });
});

describe("pushAllReviewComments", () => {
  it.each([
    ["every comment", {}, "http://localhost:8222/api/channels/ch1/review/push-all"],
    ["the blocking ones", { impact: "blocking" as const }, "http://localhost:8222/api/channels/ch1/review/push-all?impact=blocking"],
  ])("pushes %s", async (_name, opts, want) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ pushed: 1, skipped: 0, failed: 0 })));
    expect(await pushAllReviewComments("ch1", opts)).toEqual({ pushed: 1, skipped: 0, failed: 0 });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe(want);
    expect(init.method).toBe("POST");
  });
});

describe("approveReview", () => {
  it.each([
    [{}, '{"body":"","force":false}'],
    [{ body: "LGTM", force: true }, '{"body":"LGTM","force":true}'],
  ])("POSTs %o and returns the approved head", async (opts, wantBody) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ approved_sha: "abc" })));
    await expect(approveReview("ch1", opts)).resolves.toBe("abc");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/ch1/review/approve");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(wantBody);
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("outcome is request_changes", { status: 409 }));
    await expect(approveReview("ch1")).rejects.toThrow("outcome is request_changes");
  });

  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 502, statusText: "Bad Gateway" }));
    await expect(approveReview("ch1")).rejects.toThrow("Failed to approve the PR: Bad Gateway");
  });
});

// respond answers every fetch with a fresh Response, so a body can be read on each call.
function respond(body: string | null, init: ResponseInit = {}) {
  fetchMock.mockImplementation(() => Promise.resolve(new Response(body, init)));
}

describe("list endpoints", () => {
  it.each([
    ["listReviewSessions", () => listReviewSessions(), "http://localhost:8222/api/review/sessions", "sessions"],
    ["listReviewPRs", () => listReviewPRs("ch1"), "http://localhost:8222/api/channels/ch1/review/prs", "prs"],
  ] as const)("%s returns the list, or an empty one for a missing key", async (_name, run, url, key) => {
    respond(JSON.stringify({ [key]: [{ number: 1 }] }));
    await expect(run()).resolves.toEqual([{ number: 1 }]);
    expect(fetchMock.mock.calls[0]![0]).toBe(url);
    respond(JSON.stringify({ [key]: null }));
    await expect(run()).resolves.toEqual([]);
  });

  it("listReviewSessions throws with the status text", async () => {
    respond("", { status: 500, statusText: "Bad" });
    await expect(listReviewSessions()).rejects.toThrow("Failed to list review sessions: Bad");
  });

  it("listReviewPRs throws the daemon's message, or the status text", async () => {
    respond("gh not authenticated", { status: 502 });
    await expect(listReviewPRs("ch1")).rejects.toThrow("gh not authenticated");
    respond("", { status: 500, statusText: "Bad" });
    await expect(listReviewPRs("ch1")).rejects.toThrow("Failed to list PRs: Bad");
  });
});

describe("session writes", () => {
  const session = JSON.stringify({ present: true, session: { channel_id: "ch1", comments: null, status: "ready" } });

  it.each([
    ["loadReviewPR", () => loadReviewPR("ch1", 7), "review/load", "POST", '{"pr_number":7,"background":true}', "Failed to load PR: Bad"],
    ["syncReviewSession", () => syncReviewSession("ch1"), "review/sync", "POST", undefined, "Failed to sync review: Bad"],
    ["setReviewFork with a session", () => setReviewFork("ch1", "custom", "s1"), "review/fork", "PUT", '{"mode":"custom","session_id":"s1"}', "Failed to set fork mode: Bad"],
    ["setReviewFork without a session", () => setReviewFork("ch1", "current"), "review/fork", "PUT", '{"mode":"current","session_id":""}', "Failed to set fork mode: Bad"],
    ["setReviewAgent", () => setReviewAgent("ch1", "opus", "high"), "review/agent", "PUT", '{"model":"opus","effort":"high"}', "Failed to set review model: Bad"],
  ] as const)("%s sends the request and normalizes the session", async (_name, run, path, method, body, err) => {
    respond(session);
    const resp = await run();
    expect(resp.session?.comments).toEqual([]);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe(`http://localhost:8222/api/channels/ch1/${path}`);
    expect(init.method).toBe(method);
    expect(init.body).toBe(body);

    respond("busy", { status: 409 });
    await expect(run()).rejects.toThrow(/^busy$/);
    respond("", { status: 500, statusText: "Bad" });
    await expect(run()).rejects.toThrow(err);
  });

  it("keeps comments the daemon sends and passes a missing session through", async () => {
    respond(JSON.stringify({ present: true, session: { channel_id: "ch1", comments: [{ id: "c1" }], status: "ready" } }));
    expect((await syncReviewSession("ch1")).session?.comments).toEqual([{ id: "c1" }]);
    respond(JSON.stringify({ present: false }));
    expect(await syncReviewSession("ch1")).toEqual({ present: false });
  });
});

describe("deleteReviewSession", () => {
  it.each([204, 404])("treats %s as closed", async (status) => {
    respond(null, { status });
    await expect(deleteReviewSession("ch1")).resolves.toBeUndefined();
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/ch1/review");
    expect(init.method).toBe("DELETE");
  });

  it("throws on other failures", async () => {
    respond("", { status: 500, statusText: "Bad" });
    await expect(deleteReviewSession("ch1")).rejects.toThrow("Failed to close review: Bad");
  });
});

describe("run and comment actions", () => {
  it("runReview POSTs and returns the status", async () => {
    respond(JSON.stringify({ status: "started" }));
    await expect(runReview("ch1")).resolves.toEqual({ status: "started" });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/ch1/review/run");
    expect(init.method).toBe("POST");
  });

  it.each([
    ["stopReview", () => stopReview("ch1"), "http://localhost:8222/api/channels/ch1/review/stop", "POST"],
    ["pushReviewComment", () => pushReviewComment("ch1", "a/b"), "http://localhost:8222/api/channels/ch1/review/comments/a%2Fb/push", "POST"],
    ["deleteReviewComment", () => deleteReviewComment("ch1", "a/b"), "http://localhost:8222/api/channels/ch1/review/comments/a%2Fb", "DELETE"],
  ] as const)("%s sends the request", async (_name, run, url, method) => {
    respond(null, { status: 204 });
    await expect(run()).resolves.toBeUndefined();
    expect(fetchMock.mock.calls[0]![0]).toBe(url);
    expect(fetchMock.mock.calls[0]![1].method).toBe(method);
  });

  it.each([
    ["runReview", () => runReview("ch1"), "Failed to start review: Bad"],
    ["stopReview", () => stopReview("ch1"), "Failed to stop review: Bad"],
    ["pushReviewComment", () => pushReviewComment("ch1", "c1"), "Failed to push comment: Bad"],
    ["deleteReviewComment", () => deleteReviewComment("ch1", "c1"), "Failed to delete comment: Bad"],
    ["pushAllReviewComments", () => pushAllReviewComments("ch1"), "Failed to push comments: Bad"],
  ] as const)("%s throws the daemon's message, or the status text", async (_name, run, err) => {
    respond("not found", { status: 404 });
    await expect(run()).rejects.toThrow(/^not found$/);
    respond("", { status: 500, statusText: "Bad" });
    await expect(run()).rejects.toThrow(err);
  });
});
