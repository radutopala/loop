import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const claimTerminalSessions = vi.fn();
vi.mock("../api/terminal", () => ({ claimTerminalSessions }));

async function load() {
  vi.resetModules();
  return import("./terminalClaims");
}

describe("startTerminalClaims", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    claimTerminalSessions.mockReset();
    claimTerminalSessions.mockResolvedValue(undefined);
  });
  afterEach(() => vi.useRealTimers());

  it("claims now and every minute after, once per window", async () => {
    const { startTerminalClaims, CLAIM_INTERVAL_MS } = await load();
    const held = vi.fn(() => ["sess-1"]);
    startTerminalClaims(held);
    startTerminalClaims(held);
    expect(claimTerminalSessions).toHaveBeenCalledTimes(1);
    expect(claimTerminalSessions).toHaveBeenCalledWith(["sess-1"]);

    vi.advanceTimersByTime(CLAIM_INTERVAL_MS);
    expect(claimTerminalSessions).toHaveBeenCalledTimes(2);
  });

  it("skips the claim when no session is held", async () => {
    const { startTerminalClaims } = await load();
    startTerminalClaims(() => []);
    expect(claimTerminalSessions).not.toHaveBeenCalled();
  });

  it("swallows a failed claim", async () => {
    claimTerminalSessions.mockRejectedValue(new Error("offline"));
    const { startTerminalClaims } = await load();
    startTerminalClaims(() => ["sess-1"]);
    await vi.runOnlyPendingTimersAsync();
    expect(claimTerminalSessions).toHaveBeenCalled();
  });
});
