import { describe, expect, it } from "vitest";
import { promptEditRunning } from "./usePromptEdits";

describe("promptEditRunning", () => {
  it.each([
    ["running", true],
    ["completed", false],
    ["error", false],
  ] as const)("%s → %j", (status, want) => {
    expect(promptEditRunning(status)).toBe(want);
  });
});
