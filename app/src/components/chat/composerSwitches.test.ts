import { describe, expect, it } from "vitest";
import type { ComposerSwitch } from "../../api/composerSwitches";
import { switchKey, switchLook, switchTip, withSwitchState } from "./composerSwitches";

const mode: ComposerSwitch = { plugin_id: "p", switch_id: "mode", label: "Mode", on: false };

describe("withSwitchState", () => {
  it("sets the state of the switch with the key and leaves the others", () => {
    const other: ComposerSwitch = { plugin_id: "q", switch_id: "mode", on: true, status: "ok" };
    const got = withSwitchState([mode, other], "p/mode", { on: true, status: "busy", title: "Starting" });
    expect(got).toEqual([{ ...mode, on: true, status: "busy", title: "Starting" }, other]);
  });

  it("clears what the new state leaves out", () => {
    const set: ComposerSwitch = { ...mode, on: true, status: "ok", title: "Connected" };
    expect(withSwitchState([set], switchKey(set), { on: false })).toEqual([{ ...mode, on: false, status: undefined, title: undefined }]);
  });

  it("ignores a switch it doesn't list", () => {
    expect(withSwitchState([mode], "p/other", { on: true })).toEqual([mode]);
  });
});

describe("switchLook", () => {
  it.each([
    [{ on: false }, false, false, "off"],
    [{ on: true }, false, false, "on"],
    [{ on: true, status: "ok" as const }, false, false, "on"],
    [{ on: true, status: "busy" as const }, false, false, "busy"],
    [{ on: false }, true, false, "busy"],
    [{ on: true, status: "error" as const }, false, false, "error"],
    [{ on: true }, true, true, "error"],
  ])("%o pending=%s failed=%s looks %s", (state, pending, failed, want) => {
    expect(switchLook(state, pending, failed)).toBe(want);
  });
});

describe("switchTip", () => {
  it("heads with the label and says on or off without a title", () => {
    expect(switchTip(mode, undefined)).toBe("Mode\nOff");
    expect(switchTip({ ...mode, label: undefined, on: true }, undefined)).toBe("mode\nOn");
  });

  it("prefers the plugin's title to the manifest's", () => {
    expect(switchTip({ ...mode, default_title: "Mode is off" }, undefined)).toBe("Mode\nMode is off");
    expect(switchTip({ ...mode, default_title: "Mode is off", title: "Connected" }, undefined)).toBe("Mode\nConnected");
  });

  it("doesn't repeat a title that's the label, and ends with the error", () => {
    expect(switchTip({ ...mode, title: "Mode" }, "plugin down")).toBe("Mode\nplugin down");
  });
});
