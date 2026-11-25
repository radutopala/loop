import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import {
  cancelWorkflowRun,
  deleteWorkflowRun,
  FetchWorkflowRunError,
  fetchWorkflowRun,
  fetchWorkflowRuns,
  fetchWorkflows,
  resumeWorkflowRun,
  retryWorkflowRun,
  saveWorkflowDef,
  startWorkflowRun,
} from "./workflows";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchWorkflows", () => {
  it("lists the workflows, with and without a channel", async () => {
    fetchMock.mockImplementation(async () => json([{ name: "w" }]));
    await expect(fetchWorkflows("c 1")).resolves.toEqual([{ name: "w" }]);
    await fetchWorkflows();
    expect(sent(fetchMock, 0).url).toBe(`${API}/api/workflows?channel_id=c%201`);
    expect(sent(fetchMock, 1).url).toBe(`${API}/api/workflows`);
  });

  it("returns none for a null body", async () => {
    fetchMock.mockResolvedValue(json(null));
    await expect(fetchWorkflows()).resolves.toEqual([]);
  });
});

describe("error descriptions", () => {
  it("uses the JSON error field", async () => {
    fetchMock.mockResolvedValue(fail(400, "", '{"error":"bad yaml"}'));
    await expect(fetchWorkflows()).rejects.toThrow("Failed to fetch workflows: bad yaml");
  });

  it.each([
    ["a JSON body without an error string", '{"error":3}', '{"error":3}'],
    ["a JSON body with an empty error", '{"error":""}', '{"error":""}'],
    ["a JSON null", "null", "null"],
    ["a plain body", " daemon says no \n", "daemon says no"],
  ])("uses the raw text for %s", async (_name, body, want) => {
    fetchMock.mockResolvedValue(fail(400, "", body));
    await expect(fetchWorkflows()).rejects.toThrow(`Failed to fetch workflows: ${want}`);
  });

  it("cuts a long body at 400 characters", async () => {
    fetchMock.mockResolvedValue(fail(500, "", "x".repeat(401)));
    await expect(fetchWorkflows()).rejects.toThrow(`Failed to fetch workflows: ${"x".repeat(400)}…`);
  });

  it("falls back to the status text, then the code, for an empty body", async () => {
    fetchMock.mockResolvedValueOnce(fail(502, "Bad Gateway"));
    await expect(fetchWorkflows()).rejects.toThrow("Failed to fetch workflows: Bad Gateway");
    fetchMock.mockResolvedValueOnce(fail(502, ""));
    await expect(fetchWorkflows()).rejects.toThrow("Failed to fetch workflows: 502");
  });

  it("falls back to the status text, then the code, when the body can't be read", async () => {
    const lost = (statusText: string) => ({ ok: false, status: 503, statusText, text: () => Promise.reject(new Error("x")) }) as unknown as Response;
    fetchMock.mockResolvedValueOnce(lost("Unavailable"));
    await expect(cancelWorkflowRun("r")).rejects.toThrow("Failed to cancel workflow run: Unavailable");
    fetchMock.mockResolvedValueOnce(lost(""));
    await expect(cancelWorkflowRun("r")).rejects.toThrow("Failed to cancel workflow run: 503");
  });
});

describe("fetchWorkflowRuns", () => {
  it("passes the channel, limit and a positive offset", async () => {
    fetchMock.mockResolvedValue(json([{ id: "r" }]));
    await expect(fetchWorkflowRuns("c1", 10, 20)).resolves.toEqual([{ id: "r" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/workflows/runs?channel_id=c1&limit=10&offset=20`);
  });

  it("leaves out a zero offset and missing params", async () => {
    fetchMock.mockImplementation(async () => json(null));
    await expect(fetchWorkflowRuns(undefined, 0, 0)).resolves.toEqual([]);
    await fetchWorkflowRuns();
    expect(sent(fetchMock, 0).url).toBe(`${API}/api/workflows/runs?limit=0`);
    expect(sent(fetchMock, 1).url).toBe(`${API}/api/workflows/runs`);
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchWorkflowRuns()).rejects.toThrow("Failed to fetch workflow runs: boom");
  });
});

describe("fetchWorkflowRun", () => {
  it("returns the run and passes the signal", async () => {
    fetchMock.mockResolvedValue(json({ run: { id: "r/1" } }));
    const signal = new AbortController().signal;
    await expect(fetchWorkflowRun("r/1", { signal })).resolves.toEqual({ run: { id: "r/1" } });
    expect(sent(fetchMock).url).toBe(`${API}/api/workflows/runs/r%2F1`);
    expect(fetchMock.mock.calls[0]![1]?.signal).toBe(signal);
  });

  it("throws a FetchWorkflowRunError carrying the status", async () => {
    fetchMock.mockResolvedValue(fail(404, "Not Found"));
    const err = await fetchWorkflowRun("r").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(FetchWorkflowRunError);
    expect(err).toMatchObject({ status: 404, name: "FetchWorkflowRunError", message: "Failed to fetch workflow run: Not Found" });
  });

  it("names the code when the description is empty", () => {
    expect(new FetchWorkflowRunError(500, "").message).toBe("Failed to fetch workflow run: 500");
  });
});

describe("run actions", () => {
  it("starts a run", async () => {
    fetchMock.mockResolvedValue(json({ run_id: "r1" }));
    await expect(startWorkflowRun({ workflow_name: "w", inputs: { a: "1" } })).resolves.toEqual({ run_id: "r1" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/workflows/runs`, method: "POST", body: { workflow_name: "w", inputs: { a: "1" } } });
    expect(sent(fetchMock).headers.get("Content-Type")).toBe("application/json");
  });

  it("saves a definition", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await saveWorkflowDef({ action: "delete", scope: "global", name: "w" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/workflows`, method: "POST", body: { action: "delete", scope: "global", name: "w" } });
  });

  it("resumes a run", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await resumeWorkflowRun("r/1", "yes");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/workflows/runs/r%2F1/resume`, method: "POST", body: { response: "yes" } });
  });

  it("cancels a run", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await cancelWorkflowRun("r1");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/workflows/runs/r1/cancel`, method: "POST" });
  });

  it("deletes a run", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await deleteWorkflowRun("r1");
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/workflows/runs/r1`, method: "DELETE" });
  });

  it("retries a run", async () => {
    fetchMock.mockResolvedValue(json({ run_id: "r2" }));
    await expect(retryWorkflowRun("r1")).resolves.toEqual({ run_id: "r2" });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/workflows/runs/r1/retry`, method: "POST" });
  });

  it.each([
    ["startWorkflowRun", () => startWorkflowRun({ workflow_name: "w" }), "Failed to start workflow: boom"],
    ["saveWorkflowDef", () => saveWorkflowDef({ action: "add" }), "Failed to save workflow: boom"],
    ["resumeWorkflowRun", () => resumeWorkflowRun("r", ""), "Failed to resume workflow run: boom"],
    ["cancelWorkflowRun", () => cancelWorkflowRun("r"), "Failed to cancel workflow run: boom"],
    ["deleteWorkflowRun", () => deleteWorkflowRun("r"), "Failed to delete workflow run: boom"],
    ["retryWorkflowRun", () => retryWorkflowRun("r"), "Failed to retry workflow run: boom"],
  ])("%s throws the description", async (_name, run, msg) => {
    fetchMock.mockResolvedValue(fail(500, "", "boom"));
    await expect(run()).rejects.toThrow(msg);
  });
});
