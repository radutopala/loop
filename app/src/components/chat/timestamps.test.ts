import { describe, expect, it } from "vitest";
import { formatMessageTimestamp } from "./timestamps";

// The rendered strings are locale-dependent, so the expectations are built with
// the same Intl calls the component would use. What's under test is the
// branching — whether a date is prepended at all, and whether it carries a year.
const clock = (at: Date) => at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const day = (at: Date, opts: Intl.DateTimeFormatOptions = {}) => at.toLocaleDateString([], { month: "short", day: "numeric", ...opts });

describe("formatMessageTimestamp", () => {
  const now = new Date(2026, 8, 9, 14, 30); // 2026-09-09 14:30 local

  it("shows only the time for a message sent earlier today", () => {
    const at = new Date(2026, 8, 9, 9, 5);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(clock(at));
  });

  it("shows only the time at the very start of today", () => {
    const at = new Date(2026, 8, 9, 0, 0);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(clock(at));
  });

  it("puts the date before the time for yesterday", () => {
    const at = new Date(2026, 8, 8, 23, 59);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(`${day(at)} ${clock(at)}`);
  });

  it("puts the date before the time for a later hour on a previous day", () => {
    // Guards against comparing timestamps rather than calendar days: this is
    // "earlier" by date but "later" by clock than `now`.
    const at = new Date(2026, 8, 8, 18, 45);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(`${day(at)} ${clock(at)}`);
  });

  it("adds the year when the message is from a different year", () => {
    const at = new Date(2025, 11, 24, 10, 0);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(`${day(at, { year: "numeric" })} ${clock(at)}`);
  });

  it("omits the year for another day in the same year", () => {
    const at = new Date(2026, 0, 3, 10, 0);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(`${day(at)} ${clock(at)}`);
  });

  it("treats the same day in a different year as not today", () => {
    const at = new Date(2025, 8, 9, 14, 30);
    expect(formatMessageTimestamp(at.toISOString(), now)).toBe(`${day(at, { year: "numeric" })} ${clock(at)}`);
  });

  it("returns an empty string for an unparseable timestamp", () => {
    expect(formatMessageTimestamp("not a date", now)).toBe("");
  });

  it("defaults to the current time when no clock is supplied", () => {
    const at = new Date();
    expect(formatMessageTimestamp(at.toISOString())).toBe(clock(at));
  });
});
