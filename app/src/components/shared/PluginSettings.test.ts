import { describe, expect, it } from "vitest";
import { parseSetting, settingKeys, settingRange, settingText } from "./PluginSettings";

describe("settingKeys", () => {
  it("orders by x-order, then name", () => {
    expect(settingKeys({ b: { type: "string" }, a: { type: "string" }, z: { type: "string", "x-order": -1 } })).toEqual(["z", "a", "b"]);
    expect(settingKeys(undefined)).toEqual([]);
  });
});

describe("parseSetting", () => {
  it.each([
    { type: "integer", text: "", want: { value: undefined } },
    { type: "integer", text: "7", want: { value: 7 } },
    { type: "integer", text: "7.5", want: { error: '"7.5" isn\'t a whole number' } },
    { type: "number", text: "7.5", want: { value: 7.5 } },
    { type: "number", text: "x", want: { error: '"x" isn\'t a number' } },
    { type: "string", text: "7", want: { value: "7" } },
  ])("reads $text as a $type", ({ type, text, want }) => {
    expect(parseSetting({ type }, text)).toEqual(want);
  });
});

describe("settingText", () => {
  it.each([
    { v: undefined, want: "" },
    { v: null, want: "" },
    { v: "en", want: "en" },
    { v: 5, want: "5" },
    { v: true, want: "true" },
  ])("shows $v as $want", ({ v, want }) => {
    expect(settingText(v)).toBe(want);
  });
});

describe("settingRange", () => {
  it.each([
    { prop: { type: "integer", minimum: 3, maximum: 10 }, want: "3–10" },
    { prop: { type: "integer", minimum: 3 }, want: "≥ 3" },
    { prop: { type: "integer", maximum: 10 }, want: "≤ 10" },
    { prop: { type: "integer" }, want: "" },
  ])("$want", ({ prop, want }) => {
    expect(settingRange(prop)).toBe(want);
  });
});
