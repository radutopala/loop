import { describe, expect, it } from "vitest";
import { learnEffective, learnToggleTitle } from "./learnState";

describe("learnEffective", () => {
  it.each([
    ["", false, false],
    ["", true, true],
    ["on", false, true],
    ["off", true, false],
  ] as const)("learn=%j default=%j → %j", (learn, def, want) => {
    expect(learnEffective(learn, def)).toBe(want);
  });
});

describe("learnToggleTitle", () => {
  it("names the config default when the channel inherits it", () => {
    expect(learnToggleTitle("", true)).toMatch(/^Learn is on — config default \(on\)\./);
    expect(learnToggleTitle("", true)).toMatch(/Click to turn it off\.$/);
  });

  it("says when the channel has its own setting", () => {
    expect(learnToggleTitle("off", true)).toMatch(/^Learn is off — set for this channel\./);
    expect(learnToggleTitle("off", true)).toMatch(/Click to turn it on\.$/);
  });
});
