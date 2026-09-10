import { describe, expect, it } from "vitest";
import { chooseSendRoute, normalizeSendMode } from "./sendRouting";

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
    expect(chooseSendRoute({})).toEqual({ kind: "message", steer: false });
    expect(chooseSendRoute({ pendingGateReqId: null, sendMode: "queue" })).toEqual({ kind: "message", steer: false });
  });

  it("steers only when a run is active and the composer is in steer mode", () => {
    expect(chooseSendRoute({ isRunning: true, sendMode: "steer" })).toEqual({ kind: "message", steer: true });
    expect(chooseSendRoute({ isRunning: true, sendMode: "queue" })).toEqual({ kind: "message", steer: false });
    expect(chooseSendRoute({ isRunning: false, sendMode: "steer" })).toEqual({ kind: "message", steer: false });
  });
});

describe("normalizeSendMode", () => {
  it("keeps a stored steer mode", () => {
    expect(normalizeSendMode("steer")).toBe("steer");
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
