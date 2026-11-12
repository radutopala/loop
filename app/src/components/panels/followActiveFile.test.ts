import { describe, expect, it } from "vitest";
import { DEFAULT_EDITOR_CONFIG, type EditorConfig } from "../../api/loopApi";
import { followActiveFileKey, followTooltip, resolveFollow, storedFollow } from "./followActiveFile";

const projectOff: EditorConfig = { ...DEFAULT_EDITOR_CONFIG, follow_active_file: false, follow_active_file_from: "project" };

describe("followActiveFileKey", () => {
  it("is per channel", () => {
    expect(followActiveFileKey("ch-1")).toBe("loop.editor.followActive.ch-1");
  });
});

describe("resolveFollow", () => {
  it.each([
    ["the config without a toggle", DEFAULT_EDITOR_CONFIG, null, { on: true, source: "default" }],
    ["the project config", projectOff, null, { on: false, source: "project" }],
    ["the channel's toggle on", projectOff, "1", { on: true, source: "channel" }],
    ["the channel's toggle off", DEFAULT_EDITOR_CONFIG, "0", { on: false, source: "channel" }],
    ["the config over a stray value", DEFAULT_EDITOR_CONFIG, "yes", { on: true, source: "default" }],
  ] as const)("takes %s", (_, config, stored, want) => {
    expect(resolveFollow(config, stored)).toEqual(want);
  });
});

describe("storedFollow", () => {
  it.each([
    [DEFAULT_EDITOR_CONFIG, false, "0"],
    [DEFAULT_EDITOR_CONFIG, true, null],
    [projectOff, true, "1"],
    [projectOff, false, null],
  ] as const)("stores %#", (config, on, want) => {
    expect(storedFollow(config, on)).toBe(want);
  });
});

describe("followTooltip", () => {
  it("names the config layer the value comes from", () => {
    expect(followTooltip(projectOff, { on: false, source: "project" })).toBe(
      "Follow active file: off\nThe tree stays where it is as tabs change\nFrom the project config (editor.follow_active_file); a click sets it for this channel only",
    );
    expect(followTooltip(DEFAULT_EDITOR_CONFIG, { on: true, source: "default" })).toContain("From the default (editor.follow_active_file)");
  });

  it("says a channel's toggle overrides the config", () => {
    expect(followTooltip({ ...DEFAULT_EDITOR_CONFIG, follow_active_file_from: "global" }, { on: false, source: "channel" })).toBe(
      "Follow active file: off\nThe tree stays where it is as tabs change\nSet here, for this channel only; the global config (editor.follow_active_file) has it on",
    );
  });
});
