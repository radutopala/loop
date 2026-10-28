import { describe, expect, it } from "vitest";
import type { WSEvent } from "../../types";
import { applySuggestionEvent, composerPlaceholder, PROMPT_SUGGESTION_EVENT } from "./promptSuggestion";

function event(type: string, data: unknown, channel_id = "ch1"): WSEvent {
  return { type, channel_id, data, timestamp: 0 };
}

describe("applySuggestionEvent", () => {
  it.each([
    ["a suggestion sets it", null, event(PROMPT_SUGGESTION_EVENT, { suggestion: " run the tests " }), "run the tests"],
    ["a blank one keeps the last", "old", event(PROMPT_SUGGESTION_EVENT, { suggestion: "  " }), "old"],
    ["a run starting drops it", "old", event("agent.status", { status: "running" }), null],
    ["a run ending keeps it", "old", event("agent.status", { status: "completed" }), "old"],
    ["another channel's leaves it", "old", event(PROMPT_SUGGESTION_EVENT, { suggestion: "new" }, "ch2"), "old"],
    ["other events leave it", "old", event("message.created", {}), "old"],
  ])("%s", (_name, current, ev, want) => {
    expect(applySuggestionEvent(current, ev, "ch1")).toBe(want);
  });
});

describe("composerPlaceholder", () => {
  it("shows the suggestion with Tab", () => {
    expect(composerPlaceholder("run the tests", "Ask Loop")).toBe("run the tests  ⇥ Tab");
  });
  it.each([null, undefined, ""])("falls back to the hint for %s", (s) => {
    expect(composerPlaceholder(s, "Ask Loop")).toBe("Ask Loop");
  });
});
