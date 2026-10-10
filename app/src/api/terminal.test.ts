import { beforeEach, describe, expect, it, vi } from "vitest";

const apiFetch = vi.fn();
vi.mock("./api", () => ({ apiFetch, getApiUrl: () => "http://api" }));

const { claimTerminalSessions } = await import("./terminal");

describe("claimTerminalSessions", () => {
  beforeEach(() => apiFetch.mockReset());

  it("posts the session ids", async () => {
    apiFetch.mockResolvedValue(new Response(null, { status: 204 }));
    await claimTerminalSessions(["sess-1", "sess-2"]);
    expect(apiFetch).toHaveBeenCalledWith("http://api/api/terminal/claims", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_ids: ["sess-1", "sess-2"] }),
    });
  });

  it("throws when the server refuses", async () => {
    apiFetch.mockResolvedValue(new Response("bad", { status: 400, statusText: "Bad Request" }));
    await expect(claimTerminalSessions(["sess-1"])).rejects.toThrow("Failed to claim terminal sessions: Bad Request");
  });
});
