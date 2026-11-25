import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { unreadable } from "../test/hooks-c-api-fetch";
import { applyLearnProposal, dismissLearnProposal, fetchLearnPasses, fetchLearnProposalPreview, fetchLearnProposals, fetchLearnState, learnTurn, setLearn } from "./learn";

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

describe("fetchLearnProposalPreview", () => {
  it("returns the edit applying the proposal would make", async () => {
    const preview = { path: "/proj/.loop/config.json", diff: "--- /dev/null\n+++ /proj/.loop/config.json\n" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(preview)));
    await expect(fetchLearnProposalPreview(7)).resolves.toEqual(preview);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/learn/proposals/7/preview");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404, statusText: "Not Found" }));
    await expect(fetchLearnProposalPreview(7)).rejects.toThrow("Failed to preview proposal: Not Found");
  });
});

describe("fetchLearnState", () => {
  it("returns the state", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ available: true, learn: "on" })));
    await expect(fetchLearnState("c1")).resolves.toEqual({ available: true, learn: "on" });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c1/learn");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchLearnState("c1")).rejects.toThrow("Failed to fetch learn state: boom");
  });
});

describe("setLearn", () => {
  it("puts the setting", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await setLearn("c1", "off");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/c1/learn");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(init.body)).toEqual({ learn: "off" });
  });

  it("throws the server's message, or the status text when there is none or it can't be read", async () => {
    fetchMock.mockResolvedValueOnce(new Response(" not here \n", { status: 400, statusText: "Bad Request" }));
    await expect(setLearn("c1", "on")).rejects.toThrow(/^not here$/);
    fetchMock.mockResolvedValueOnce(new Response("", { status: 400, statusText: "Bad Request" }));
    await expect(setLearn("c1", "on")).rejects.toThrow("Failed to set learn: Bad Request");
    fetchMock.mockResolvedValueOnce(unreadable(500, "boom"));
    await expect(setLearn("c1", "on")).rejects.toThrow("Failed to set learn: boom");
  });
});

describe("fetchLearnPasses", () => {
  it("returns the passes", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ passes: [{ id: 1 }] })));
    await expect(fetchLearnPasses("c1")).resolves.toEqual([{ id: 1 }]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c1/learn/passes");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchLearnPasses("c1")).rejects.toThrow("Failed to fetch learn passes: boom");
  });
});

describe("learnTurn", () => {
  it("posts the turn's message id", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ id: 2, status: "queued" })));
    await expect(learnTurn("c1", "m1")).resolves.toEqual({ id: 2, status: "queued" });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/c1/learn/passes");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ message_id: "m1" });
  });

  it("throws the server's message, or the status text when there is none or it can't be read", async () => {
    fetchMock.mockResolvedValueOnce(new Response("learn is off\n", { status: 409, statusText: "Conflict" }));
    await expect(learnTurn("c1", "m1")).rejects.toThrow(/^learn is off$/);
    fetchMock.mockResolvedValueOnce(new Response("", { status: 409, statusText: "Conflict" }));
    await expect(learnTurn("c1", "m1")).rejects.toThrow("Failed to learn from the turn: Conflict");
    fetchMock.mockResolvedValueOnce(unreadable(500, "boom"));
    await expect(learnTurn("c1", "m1")).rejects.toThrow("Failed to learn from the turn: boom");
  });
});

describe("fetchLearnProposals", () => {
  it("returns the proposals", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ proposals: [{ id: 3 }] })));
    await expect(fetchLearnProposals("c1")).resolves.toEqual([{ id: 3 }]);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/c1/learn/proposals");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "boom" }));
    await expect(fetchLearnProposals("c1")).rejects.toThrow("Failed to fetch learn proposals: boom");
  });
});

describe.each([
  ["apply", applyLearnProposal],
  ["dismiss", dismissLearnProposal],
] as const)("%s a proposal", (action, settle) => {
  it("posts to the action's route", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ id: 4, status: "applied" })));
    await expect(settle(4)).resolves.toEqual({ id: 4, status: "applied" });
    expect(fetchMock.mock.calls[0]![0]).toBe(`http://localhost:8222/api/learn/proposals/4/${action}`);
    expect(fetchMock.mock.calls[0]![1].method).toBe("POST");
  });

  it("throws the server's message, or the status text when there is none or it can't be read", async () => {
    fetchMock.mockResolvedValueOnce(new Response("already settled", { status: 409, statusText: "Conflict" }));
    await expect(settle(4)).rejects.toThrow("already settled");
    fetchMock.mockResolvedValueOnce(new Response("", { status: 409, statusText: "Conflict" }));
    await expect(settle(4)).rejects.toThrow(`Failed to ${action} proposal: Conflict`);
    fetchMock.mockResolvedValueOnce(unreadable(500, "boom"));
    await expect(settle(4)).rejects.toThrow(`Failed to ${action} proposal: boom`);
  });
});
