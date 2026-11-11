import { describe, expect, it } from "vitest";
import { DICTATION_HOLD_CODE, DICTATION_HOLD_MS, type HoldTimers, HoldToTalk } from "./dictationHold";

/** Manual timers: run() fires what's due by the time given. */
function fakeTimers() {
  let now = 0;
  let next = 0;
  const pending = new Map<number, { at: number; fn: () => void }>();
  const timers: HoldTimers = {
    set: (fn, ms) => {
      pending.set(++next, { at: now + ms, fn });
      return next;
    },
    clear: (h) => pending.delete(h as number),
  };
  const advance = (ms: number) => {
    now += ms;
    for (const [id, t] of [...pending]) {
      if (t.at <= now) {
        pending.delete(id);
        t.fn();
      }
    }
  };
  return { timers, advance, pending };
}

function make() {
  const calls: string[] = [];
  const t = fakeTimers();
  const hold = new HoldToTalk(
    {
      prepare: () => calls.push("prepare"),
      start: () => calls.push("start"),
      stop: () => calls.push("stop"),
      cancel: () => calls.push("cancel"),
      abort: () => calls.push("abort"),
    },
    t.timers,
  );
  return { hold, calls, ...t };
}

const alt = { code: DICTATION_HOLD_CODE, key: "Alt" };
const key = (k: string, code = `Key${k.toUpperCase()}`) => ({ code, key: k });

describe("HoldToTalk", () => {
  it("defaults to holding Right Alt / Right Option for 200 ms", () => {
    expect(DICTATION_HOLD_CODE).toBe("AltRight");
    expect(DICTATION_HOLD_MS).toBe(200);
  });

  it("prepares on key-down, starts after the hold, stops on release", () => {
    const { hold, calls, advance } = make();
    expect(hold.keyDown(alt)).toBe(false);
    expect(calls).toEqual(["prepare"]);
    advance(DICTATION_HOLD_MS - 1);
    expect(calls).toEqual(["prepare"]);
    advance(1);
    expect(calls).toEqual(["prepare", "start"]);
    expect(hold.active).toBe(true);
    expect(hold.keyUp(alt)).toBe(true);
    expect(calls).toEqual(["prepare", "start", "stop"]);
    expect(hold.busy).toBe(false);
  });

  it("aborts a tap released before the hold", () => {
    const { hold, calls, advance, pending } = make();
    hold.keyDown(alt);
    advance(100);
    expect(hold.keyUp(alt)).toBe(false);
    expect(calls).toEqual(["prepare", "abort"]);
    expect(pending.size).toBe(0);
    advance(500);
    expect(calls).toEqual(["prepare", "abort"]);
  });

  it("aborts, and lets the key through, when another key comes during the hold (an Option combo)", () => {
    const { hold, calls, advance } = make();
    hold.keyDown(alt);
    expect(hold.keyDown(key("e"))).toBe(false);
    advance(500);
    expect(calls).toEqual(["prepare", "abort"]);
    // The release that follows is nothing.
    expect(hold.keyUp(alt)).toBe(false);
    expect(calls).toEqual(["prepare", "abort"]);
  });

  it("cancels, and takes the key, when another key comes after the start", () => {
    const { hold, calls, advance } = make();
    hold.keyDown(alt);
    advance(DICTATION_HOLD_MS);
    expect(hold.keyDown(key("Enter", "Enter"))).toBe(true);
    expect(calls).toEqual(["prepare", "start", "cancel"]);
    expect(hold.keyUp(alt)).toBe(false);
  });

  it("cancels on Esc", () => {
    const { hold, calls, advance } = make();
    hold.keyDown(alt);
    advance(DICTATION_HOLD_MS);
    expect(hold.keyDown({ code: "Escape", key: "Escape" })).toBe(true);
    expect(calls).toEqual(["prepare", "start", "cancel"]);
  });

  it("doesn't arm with another modifier down (AltGr reports Ctrl on Windows) or on auto-repeat", () => {
    const { hold, calls } = make();
    expect(hold.keyDown({ ...alt, ctrlKey: true })).toBe(false);
    expect(hold.keyDown({ ...alt, metaKey: true })).toBe(false);
    expect(hold.keyDown({ ...alt, shiftKey: true })).toBe(false);
    expect(hold.keyDown({ ...alt, repeat: true })).toBe(false);
    expect(hold.keyDown({ code: "AltLeft", key: "Alt" })).toBe(false);
    expect(calls).toEqual([]);
  });

  it("takes auto-repeats of the held key without restarting", () => {
    const { hold, calls, advance } = make();
    hold.keyDown(alt);
    advance(DICTATION_HOLD_MS);
    expect(hold.keyDown({ ...alt, repeat: true })).toBe(true);
    expect(calls).toEqual(["prepare", "start"]);
  });

  it("takes a lost focus as the release", () => {
    const { hold, calls, advance } = make();
    hold.keyDown(alt);
    hold.blur();
    expect(calls).toEqual(["prepare", "abort"]);
    hold.keyDown(alt);
    advance(DICTATION_HOLD_MS);
    hold.blur();
    expect(calls).toEqual(["prepare", "abort", "prepare", "start", "stop"]);
    hold.blur();
    expect(calls).toHaveLength(5);
  });

  it("starts the button at once and, held, stops on release; only Esc cancels it", () => {
    const { hold, calls, advance } = make();
    hold.pointerDown();
    expect(calls).toEqual(["prepare", "start"]);
    // Typing, or the hold key, doesn't end a button hold.
    expect(hold.keyDown(key("a"))).toBe(false);
    expect(hold.keyDown(alt)).toBe(false);
    expect(hold.keyUp(alt)).toBe(false);
    hold.blur();
    advance(DICTATION_HOLD_MS);
    hold.pointerUp();
    expect(calls).toEqual(["prepare", "start", "stop"]);
    expect(hold.busy).toBe(false);

    hold.pointerDown();
    expect(hold.keyDown({ code: "Escape", key: "Escape" })).toBe(true);
    hold.pointerUp();
    expect(calls).toEqual(["prepare", "start", "stop", "prepare", "start", "cancel"]);

    hold.pointerDown();
    hold.pointerCancel();
    expect(calls.slice(-1)).toEqual(["cancel"]);
  });

  it("latches on a click and stops on the next press", () => {
    const { hold, calls, advance, pending } = make();
    hold.pointerDown();
    hold.pointerUp();
    expect(hold.latchedOn).toBe(true);
    expect(hold.active).toBe(true);
    expect(pending.size).toBe(0);
    // Still on: time, typing, blur and a stray release or cancel leave it.
    advance(10 * DICTATION_HOLD_MS);
    expect(hold.keyDown(key("a"))).toBe(false);
    hold.blur();
    hold.pointerUp();
    hold.pointerCancel();
    expect(calls).toEqual(["prepare", "start"]);

    hold.pointerDown();
    expect(calls).toEqual(["prepare", "start", "stop"]);
    expect(hold.latchedOn).toBe(false);
    // That press's release doesn't start or stop anything.
    hold.pointerUp();
    expect(calls).toHaveLength(3);
    expect(hold.busy).toBe(false);
  });

  it("cancels a latched mic on Esc", () => {
    const { hold, calls } = make();
    hold.pointerDown();
    hold.pointerUp();
    expect(hold.keyDown({ code: "Escape", key: "Escape" })).toBe(true);
    expect(calls).toEqual(["prepare", "start", "cancel"]);
    expect(hold.latchedOn).toBe(false);
  });

  it("drops a pointer session on release() and leaves a key hold alone", () => {
    const { hold, calls, advance } = make();
    hold.pointerDown();
    hold.pointerUp();
    hold.release();
    expect(hold.busy).toBe(false);
    hold.pointerDown();
    expect(calls).toEqual(["prepare", "start", "prepare", "start"]);
    hold.release();

    hold.keyDown(alt);
    advance(DICTATION_HOLD_MS);
    hold.release();
    expect(hold.active).toBe(true);
    hold.keyUp(alt);
    expect(calls.slice(-1)).toEqual(["stop"]);
  });

  it("ignores a button press during a key hold and the reverse", () => {
    const { hold, calls, advance } = make();
    hold.keyDown(alt);
    advance(DICTATION_HOLD_MS);
    hold.pointerDown();
    hold.pointerUp();
    hold.pointerCancel();
    expect(calls).toEqual(["prepare", "start"]);
  });
});
