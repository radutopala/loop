import { describe, expect, it } from "vitest";
import type { WSEvent } from "../types";
import { applyEvent, confirmedIdle, createEmptyState } from "./useChatStateStore";

function runningState() {
  const state = createEmptyState();
  applyEvent(state, {
    type: "agent.status",
    channel_id: "ch1",
    data: { status: "running", run_id: "run-1", msg_id: "m1" },
    timestamp: 0,
  } as WSEvent);
  return state;
}

function processed(msgIds: string[]): WSEvent {
  return { type: "messages.processed", channel_id: "ch1", data: { msg_ids: msgIds }, timestamp: 0 } as WSEvent;
}

describe("applyEvent messages.processed", () => {
  // A channel that finishes while another one is open only sees its events
  // through the store; if agent.status "done" is missed, messages.processed
  // must still clear the stop button before the view remounts from it.
  const cases: { name: string; msgIds: string[]; wantProcessing: string | null }[] = [
    { name: "the turn's own message", msgIds: ["m1"], wantProcessing: null },
    { name: "a different message", msgIds: ["m2"], wantProcessing: "m1" },
  ];
  for (const tc of cases) {
    it(`clears the run for ${tc.name}`, () => {
      const state = runningState();
      expect(state.isRunning).toBe(true);
      applyEvent(state, processed(tc.msgIds));
      expect(state.isRunning).toBe(false);
      expect(state.runId).toBeNull();
      expect(state.processingMsgId).toBe(tc.wantProcessing);
    });
  }
});

describe("confirmedIdle", () => {
  // Only a fetch that started after the last "running" event can prove the
  // run ended; an older one may predate the run and hide a live Stop.
  const cases: { name: string; agentRunning: boolean; fetchStartedAt: number; lastRunningAt: number | undefined; want: boolean }[] = [
    { name: "idle, fetched after the run started", agentRunning: false, fetchStartedAt: 200, lastRunningAt: 100, want: true },
    { name: "idle, no run seen", agentRunning: false, fetchStartedAt: 200, lastRunningAt: undefined, want: true },
    { name: "idle, fetched before the run started", agentRunning: false, fetchStartedAt: 100, lastRunningAt: 200, want: false },
    { name: "idle, fetched at the same instant", agentRunning: false, fetchStartedAt: 100, lastRunningAt: 100, want: false },
    { name: "still running", agentRunning: true, fetchStartedAt: 200, lastRunningAt: 100, want: false },
  ];
  for (const tc of cases) {
    it(tc.name, () => {
      expect(confirmedIdle(tc.agentRunning, tc.fetchStartedAt, tc.lastRunningAt)).toBe(tc.want);
    });
  }
});
