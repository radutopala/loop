import { describe, expect, it } from "vitest";
import { chooseSendRoute, MODIFIER_TAP_MS, ModifierTap, nextSendMode, normalizeSendMode, oneOffSendHint, oneOffSendMode } from "./sendRouting";

describe("chooseSendRoute", () => {
  it("routes to the ask resolver while the channel is parked on a question", () => {
    expect(chooseSendRoute({ hasPendingAskUser: true })).toEqual({ kind: "ask" });
  });

  it("routes a prompt-shortcut-shaped send to the ask resolver too", () => {
    // Regression: shortcuts used to bypass the park and queue behind it, so
    // the channel stayed blocked with no card left to answer.
    expect(chooseSendRoute({ hasPendingAskUser: true, isRunning: false, sendMode: "queue" })).toEqual({ kind: "ask" });
  });

  it("routes to the plan resolver while the channel is parked on a plan", () => {
    expect(chooseSendRoute({ hasPendingExitPlan: true })).toEqual({ kind: "plan" });
  });

  it("prefers the ask park over a plan park, matching the card the chat renders", () => {
    expect(chooseSendRoute({ hasPendingAskUser: true, hasPendingExitPlan: true })).toEqual({ kind: "ask" });
  });

  it("prefers a park over a pending gate approval", () => {
    expect(chooseSendRoute({ hasPendingAskUser: true, pendingGateReqId: "req-1" })).toEqual({ kind: "ask" });
    expect(chooseSendRoute({ hasPendingExitPlan: true, pendingGateReqId: "req-1" })).toEqual({ kind: "plan" });
  });

  it("denies a pending gate approval when nothing is parked", () => {
    expect(chooseSendRoute({ pendingGateReqId: "req-1" })).toEqual({ kind: "gate", reqId: "req-1" });
  });

  it("sends a plain message when the channel is idle", () => {
    expect(chooseSendRoute({})).toEqual({ kind: "message", delivery: "queue" });
    expect(chooseSendRoute({ pendingGateReqId: null, sendMode: "queue" })).toEqual({ kind: "message", delivery: "queue" });
  });

  it("steers only when a run is active and the composer is in steer mode", () => {
    expect(chooseSendRoute({ isRunning: true, sendMode: "steer" })).toEqual({ kind: "message", delivery: "steer" });
    expect(chooseSendRoute({ isRunning: true, sendMode: "queue" })).toEqual({ kind: "message", delivery: "queue" });
    expect(chooseSendRoute({ isRunning: false, sendMode: "steer" })).toEqual({ kind: "message", delivery: "queue" });
  });

  it("adds to the running turn only when a run is active and the composer is in add mode", () => {
    expect(chooseSendRoute({ isRunning: true, sendMode: "add" })).toEqual({ kind: "message", delivery: "add" });
    expect(chooseSendRoute({ isRunning: false, sendMode: "add" })).toEqual({ kind: "message", delivery: "queue" });
  });
});

describe("normalizeSendMode", () => {
  it("keeps a stored steer mode", () => {
    expect(normalizeSendMode("steer")).toBe("steer");
  });

  it("keeps a stored add mode", () => {
    expect(normalizeSendMode("add")).toBe("add");
  });

  it("reads the pre-rename name as steer", () => {
    expect(normalizeSendMode("interrupt")).toBe("steer");
  });

  it("falls back to queue for anything else", () => {
    expect(normalizeSendMode("queue")).toBe("queue");
    expect(normalizeSendMode(null)).toBe("queue");
    expect(normalizeSendMode(undefined)).toBe("queue");
    expect(normalizeSendMode("nonsense")).toBe("queue");
  });
});

describe("oneOffSendMode", () => {
  it("leaves a plain Enter to the chosen mode", () => {
    expect(oneOffSendMode("add", {})).toBeUndefined();
  });

  it("steers with ⌘ or Ctrl, or queues when steer is chosen", () => {
    expect(oneOffSendMode("queue", { metaKey: true })).toBe("steer");
    expect(oneOffSendMode("add", { ctrlKey: true })).toBe("steer");
    expect(oneOffSendMode("steer", { metaKey: true })).toBe("queue");
  });

  it("adds with Alt, or queues when add is chosen", () => {
    expect(oneOffSendMode("queue", { altKey: true })).toBe("add");
    expect(oneOffSendMode("steer", { altKey: true })).toBe("add");
    expect(oneOffSendMode("add", { altKey: true })).toBe("queue");
  });

  it("lets ⌘ or Ctrl win over Alt", () => {
    expect(oneOffSendMode("queue", { metaKey: true, altKey: true })).toBe("steer");
  });
});

describe("oneOffSendHint", () => {
  it("names both one-off sends with the platform's keys", () => {
    expect(oneOffSendHint("queue", true)).toBe("⌘Enter to steer, ⌥Enter to add");
    expect(oneOffSendHint("add", false)).toBe("Ctrl+Enter to steer, Alt+Enter to queue");
    expect(oneOffSendHint("steer", true)).toBe("⌘Enter to queue, ⌥Enter to add");
  });
});

describe("nextSendMode", () => {
  it.each([
    ["queue", "add"],
    ["add", "steer"],
    ["steer", "queue"],
  ] as const)("cycles %s to %s", (from, to) => {
    expect(nextSendMode(from)).toBe(to);
  });
});

describe("ModifierTap", () => {
  const make = () => {
    let t = 0;
    const tap = new ModifierTap("Meta", () => t);
    return { tap, at: (ms: number) => (t = ms) };
  };

  it("counts a quick lone press and release", () => {
    const { tap, at } = make();
    tap.down({ key: "Meta" });
    at(MODIFIER_TAP_MS);
    expect(tap.up({ key: "Meta" })).toBe(true);
    // The tap is consumed; a stray second keyup is not another one.
    expect(tap.up({ key: "Meta" })).toBe(false);
  });

  it("ignores a press held too long", () => {
    const { tap, at } = make();
    tap.down({ key: "Meta" });
    at(MODIFIER_TAP_MS + 1);
    expect(tap.up({ key: "Meta" })).toBe(false);
  });

  it("ignores a chord such as ⌘C", () => {
    const { tap } = make();
    tap.down({ key: "Meta" });
    tap.down({ key: "c" });
    expect(tap.up({ key: "Meta" })).toBe(false);
  });

  it("keeps the first press time across auto-repeat", () => {
    const { tap, at } = make();
    tap.down({ key: "Meta" });
    at(MODIFIER_TAP_MS + 1);
    tap.down({ key: "Meta", repeat: true });
    expect(tap.up({ key: "Meta" })).toBe(false);
  });

  it("ignores other keys and a cancelled press", () => {
    const { tap } = make();
    expect(tap.up({ key: "Control" })).toBe(false);
    tap.down({ key: "Control" });
    expect(tap.up({ key: "Control" })).toBe(false);
    tap.down({ key: "Meta" });
    tap.cancel();
    expect(tap.up({ key: "Meta" })).toBe(false);
  });

  it("defaults to the wall clock", () => {
    const tap = new ModifierTap("Control");
    tap.down({ key: "Control" });
    expect(tap.up({ key: "Control" })).toBe(true);
  });
});
