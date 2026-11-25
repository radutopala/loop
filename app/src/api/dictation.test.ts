import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { fetchDictationStatus, openDictationSocket, startDictationModelDownload } from "./dictation";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("dictation", () => {
  it("fetches the status", async () => {
    fetchMock.mockResolvedValue(json({ enabled: true }));
    await expect(fetchDictationStatus()).resolves.toEqual({ enabled: true });
    expect(sent(fetchMock).url).toBe(`${API}/api/dictation/status`);
  });

  it("throws when the status fails", async () => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(fetchDictationStatus()).rejects.toThrow("Failed to fetch dictation status: boom");
  });

  it("starts the model download", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 202 }));
    await startDictationModelDownload();
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/dictation/model`, method: "POST" });
  });

  it("throws the server text or the status text when the download can't start", async () => {
    fetchMock.mockResolvedValueOnce(fail(409, "Conflict", "already downloading\n"));
    await expect(startDictationModelDownload()).rejects.toThrow(/^already downloading$/);
    fetchMock.mockResolvedValueOnce(fail(409, "Conflict"));
    await expect(startDictationModelDownload()).rejects.toThrow("Failed to start the model download: Conflict");
    fetchMock.mockResolvedValueOnce({ ok: false, status: 500, statusText: "boom", text: () => Promise.reject(new Error("x")) } as unknown as Response);
    await expect(startDictationModelDownload()).rejects.toThrow("Failed to start the model download: boom");
  });

  it("opens the stream socket with the auth subprotocols", () => {
    const made: Array<{ url: string; protocols: string[] }> = [];
    class FakeWs {
      binaryType = "blob";
      constructor(url: string, protocols: string[]) {
        made.push({ url, protocols });
      }
    }
    vi.stubGlobal("WebSocket", FakeWs);
    const ws = openDictationSocket("en US");
    expect(ws.binaryType).toBe("arraybuffer");
    openDictationSocket();
    expect(made).toEqual([
      { url: "ws://localhost:8222/api/dictation/stream?language=en%20US", protocols: ["loop"] },
      { url: "ws://localhost:8222/api/dictation/stream", protocols: ["loop"] },
    ]);
  });
});
