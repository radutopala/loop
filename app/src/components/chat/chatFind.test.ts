import { describe, expect, it } from "vitest";
import { locateMatch, matchCountLabel, stepMatch } from "./chatFind";

describe("stepMatch", () => {
  it.each([
    { name: "steps to an older match", active: 0, delta: 1, count: 3, want: 1 },
    { name: "steps back to a newer match", active: 2, delta: -1, count: 3, want: 1 },
    { name: "wraps past the oldest match to the newest", active: 2, delta: 1, count: 3, want: 0 },
    { name: "wraps past the newest match to the oldest", active: 0, delta: -1, count: 3, want: 2 },
    { name: "stays put on a single match", active: 0, delta: 1, count: 1, want: 0 },
    { name: "has nowhere to go without matches", active: 0, delta: -1, count: 0, want: 0 },
  ])("$name", ({ active, delta, count, want }) => {
    expect(stepMatch(active, delta, count)).toBe(want);
  });
});

describe("matchCountLabel", () => {
  it("shows the position among the matches", () => {
    expect(matchCountLabel(0, 11)).toBe("1 / 11");
    expect(matchCountLabel(10, 11)).toBe("11 / 11");
  });

  it("says when nothing matched", () => {
    expect(matchCountLabel(0, 0)).toBe("no matches");
  });
});

describe("locateMatch", () => {
  it.each([
    { name: "finds the term in one node", texts: ["see agentgate rules"], term: "agentgate", want: { start: { node: 0, offset: 4 }, end: { node: 0, offset: 13 } } },
    { name: "ignores case", texts: ["AgentGate"], term: "agentgate", want: { start: { node: 0, offset: 0 }, end: { node: 0, offset: 9 } } },
    { name: "spans nodes", texts: ["the agent", "gate", " file"], term: "agentgate", want: { start: { node: 0, offset: 4 }, end: { node: 1, offset: 4 } } },
    { name: "starts in the next node when the term begins at a boundary", texts: ["see ", "agentgate"], term: "agentgate", want: { start: { node: 1, offset: 0 }, end: { node: 1, offset: 9 } } },
    { name: "takes the first occurrence", texts: ["gate", "gate"], term: "gate", want: { start: { node: 0, offset: 0 }, end: { node: 0, offset: 4 } } },
    { name: "is null when the text lacks the term", texts: ["a link"], term: "agentgate", want: null },
    { name: "is null for an empty term", texts: ["text"], term: "", want: null },
  ])("$name", ({ texts, term, want }) => {
    expect(locateMatch(texts, term)).toEqual(want);
  });
});
