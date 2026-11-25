import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { unreadable } from "../test/hooks-c-api-fetch";
import { editPrompt, fetchPromptEditState, onPromptEditSettled } from "./promptEdit";

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

describe("editPrompt", () => {
  const req = { draft: "fix it", start: 0, end: 0, instruction: "shorter" };

  it("posts the draft and returns the edited one", async () => {
    const out = { text: "Fix.", from: 0, to: 4 };
    fetchMock.mockResolvedValue(new Response(JSON.stringify(out)));
    const signal = new AbortController().signal;
    await expect(editPrompt("ch 1", req, signal)).resolves.toEqual(out);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://localhost:8222/api/channels/ch%201/prompt-edit");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual(req);
    expect(init.signal).toBe(signal);
  });

  it("throws the daemon's message", async () => {
    fetchMock.mockResolvedValue(new Response("the instruction is empty\n", { status: 400 }));
    await expect(editPrompt("ch", req)).rejects.toThrow("the instruction is empty");
  });

  it("falls back to the status when the body is empty", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 500, statusText: "Internal Server Error" }));
    await expect(editPrompt("ch", req)).rejects.toThrow("Internal Server Error");
    fetchMock.mockResolvedValue(new Response("", { status: 502 }));
    await expect(editPrompt("ch", req)).rejects.toThrow("HTTP 502");
  });
});

describe("onPromptEditSettled", () => {
  const req = { draft: "", start: 0, end: 0, instruction: "i" };

  it("hears each edit end, done or failed, until unsubscribed", async () => {
    const heard: string[] = [];
    const off = onPromptEditSettled((id) => heard.push(id));
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ text: "x", from: 0, to: 1 })));
    await editPrompt("a", req);
    fetchMock.mockRejectedValueOnce(new Error("offline"));
    await expect(editPrompt("b", req)).rejects.toThrow("offline");
    off();
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ text: "x", from: 0, to: 1 })));
    await editPrompt("c", req);
    expect(heard).toEqual(["a", "b"]);
  });
});

describe("fetchPromptEditState", () => {
  it("gets the channel's prompt-edit thread", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ edit_channel_id: "prompt-edit-1" })));
    await expect(fetchPromptEditState("ch 1")).resolves.toEqual({ edit_channel_id: "prompt-edit-1" });
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/channels/ch%201/prompt-edit");
  });

  it("throws on an error status", async () => {
    fetchMock.mockResolvedValue(new Response("", { status: 404 }));
    await expect(fetchPromptEditState("ch")).rejects.toThrow("HTTP 404");
  });
});

describe("editPrompt with an unreadable error body", () => {
  it("falls back to the status text", async () => {
    fetchMock.mockResolvedValue(unreadable(500, "Internal Server Error"));
    await expect(editPrompt("ch", { draft: "", start: 0, end: 0, instruction: "x" })).rejects.toThrow("Internal Server Error");
  });
});
