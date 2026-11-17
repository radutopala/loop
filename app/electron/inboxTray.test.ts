import { describe, expect, it } from "vitest";
import { FLASH_MS, Flasher, inboxBounds, parseInboxMove, parseInboxReport, trayTitle } from "./inboxTray";

describe("inboxBounds", () => {
  const work = { x: 0, y: 25, width: 1440, height: 875 };
  const size = { width: 380, height: 520 };
  it.each([
    ["centred under the icon", { x: 1000, y: 0, width: 24, height: 24 }, work, { x: 822, y: 29, width: 380, height: 520 }],
    ["kept off the right edge", { x: 1420, y: 0, width: 24, height: 24 }, work, { x: 1060, y: 29, width: 380, height: 520 }],
    ["kept off the left edge", { x: 10, y: 0, width: 24, height: 24 }, work, { x: 0, y: 29, width: 380, height: 520 }],
    ["under the menu bar when the icon's bounds are unknown", { x: 0, y: 0, width: 0, height: 0 }, work, { x: 0, y: 29, width: 380, height: 520 }],
    ["shortened to a short display", { x: 1000, y: 0, width: 24, height: 24 }, { ...work, height: 400 }, { x: 822, y: 29, width: 380, height: 396 }],
  ])("%s", (_name, tray, area, want) => {
    expect(inboxBounds(tray, size, area)).toEqual(want);
  });
});

describe("trayTitle", () => {
  it.each([
    [0, ""],
    [3, "3"],
  ])("%i", (count, want) => {
    expect(trayTitle(count)).toBe(want);
  });
});

describe("parseInboxReport", () => {
  it.each([
    ["a report", { count: 2.7, flash: true }, { count: 2, flash: true }],
    ["nothing", null, null],
    ["not an object", "2", null],
    ["a negative count", { count: -1, flash: false }, null],
    ["an endless count", { count: Number.POSITIVE_INFINITY, flash: false }, null],
    ["no flash", { count: 1 }, null],
  ])("%s", (_name, msg, want) => {
    expect(parseInboxReport(msg)).toEqual(want);
  });
});

describe("parseInboxMove", () => {
  it.each([
    ["a position", { x: 10.4, y: -20.6 }, { x: 10, y: -21 }],
    ["nothing", null, null],
    ["not an object", "1,2", null],
    ["a missing y", { x: 1 }, null],
    ["an endless x", { x: Number.NaN, y: 1 }, null],
    ["an endless y", { x: 1, y: Number.POSITIVE_INFINITY }, null],
  ])("%s", (_name, msg, want) => {
    expect(parseInboxMove(msg)).toEqual(want);
  });
});

describe("Flasher", () => {
  function setup() {
    const shown: boolean[] = [];
    const ticks: (() => void)[] = [];
    const cleared: unknown[] = [];
    const f = new Flasher((alert) => shown.push(alert), {
      setInterval: (fn, ms) => {
        expect(ms).toBe(FLASH_MS);
        ticks.push(fn);
        return ticks.length;
      },
      clearInterval: (id) => cleared.push(id),
    });
    return { f, shown, ticks, cleared };
  }

  it("switches the icon while on and leaves it plain when off", () => {
    const { f, shown, ticks, cleared } = setup();
    f.set(true);
    expect(f.flashing).toBe(true);
    ticks[0]!();
    ticks[0]!();
    f.set(true);
    expect(ticks).toHaveLength(1);
    f.set(false);
    expect(f.flashing).toBe(false);
    expect(cleared).toEqual([1]);
    expect(shown).toEqual([true, false, true, false]);
  });

  it("does nothing when turned off while off", () => {
    const { f, shown, cleared } = setup();
    f.set(false);
    expect(shown).toEqual([]);
    expect(cleared).toEqual([]);
  });
});
