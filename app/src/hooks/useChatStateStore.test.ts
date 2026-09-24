import { describe, expect, it } from "vitest";
import type { WSEvent } from "../types";
import { applyEvent, createEmptyState } from "./useChatStateStore";

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
