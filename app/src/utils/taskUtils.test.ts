import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { datetimeLocalToRFC3339, defaultScheduleForType, intervalPartsToString, nextRunLabel, parseIntervalToParts, rfc3339ToDatetimeLocal, TYPE_COLORS, timeAgo } from "./taskUtils";

const NOW = new Date("2026-06-10T12:00:00Z");

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
});

function shifted(ms: number): string {
  return new Date(NOW.getTime() + ms).toISOString();
}

describe("timeAgo", () => {
  it("returns '-' for an unparseable date", () => {
    expect(timeAgo("not-a-date")).toBe("-");
  });

  it("formats minutes ago", () => {
    expect(timeAgo(shifted(-5 * 60_000))).toBe("5m ago");
    expect(timeAgo(shifted(0))).toBe("0m ago");
  });

  it("formats hours ago past 60 minutes", () => {
    expect(timeAgo(shifted(-90 * 60_000))).toBe("1h ago");
    expect(timeAgo(shifted(-23 * 3_600_000))).toBe("23h ago");
  });

  it("formats days ago past 24 hours", () => {
    expect(timeAgo(shifted(-25 * 3_600_000))).toBe("1d ago");
    expect(timeAgo(shifted(-72 * 3_600_000))).toBe("3d ago");
  });

  it("labels future timestamps as 'in Nm'", () => {
    expect(timeAgo(shifted(10 * 60_000))).toBe("in 10m");
  });
});

describe("nextRunLabel", () => {
  it("returns '-' for an unparseable date", () => {
    expect(nextRunLabel("garbage")).toBe("-");
  });

  it("returns 'overdue' for past timestamps", () => {
    expect(nextRunLabel(shifted(-1))).toBe("overdue");
  });

  it("formats upcoming minutes, hours, and days", () => {
    expect(nextRunLabel(shifted(5 * 60_000))).toBe("in 5m");
    expect(nextRunLabel(shifted(90 * 60_000))).toBe("in 1h");
    expect(nextRunLabel(shifted(48 * 3_600_000))).toBe("in 2d");
  });
});

describe("TYPE_COLORS", () => {
  it("covers the three schedule types", () => {
    expect(Object.keys(TYPE_COLORS).sort()).toEqual(["cron", "interval", "manual", "once"]);
  });
});

describe("defaultScheduleForType", () => {
  it("seeds cron and interval with sensible defaults", () => {
    expect(defaultScheduleForType("cron")).toBe("*/30 * * * *");
    expect(defaultScheduleForType("interval")).toBe("30m");
  });

  it("returns empty for once (picker-filled) and manual (no schedule)", () => {
    expect(defaultScheduleForType("once")).toBe("");
    expect(defaultScheduleForType("manual")).toBe("");
  });
});

describe("once datetime conversion", () => {
  it("returns empty for empty/unparseable input", () => {
    expect(rfc3339ToDatetimeLocal("")).toBe("");
    expect(rfc3339ToDatetimeLocal("not-a-date")).toBe("");
    expect(datetimeLocalToRFC3339("")).toBe("");
    expect(datetimeLocalToRFC3339("not-a-date")).toBe("");
  });

  it("produces a minute-precision datetime-local string", () => {
    // 09:00 UTC rendered in local time, to "YYYY-MM-DDTHH:mm" (no seconds/zone).
    expect(rfc3339ToDatetimeLocal("2026-06-11T09:00:00Z")).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
  });

  it("emits RFC3339 UTC with a Z suffix and no milliseconds", () => {
    const out = datetimeLocalToRFC3339("2026-06-11T09:00");
    expect(out).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
  });

  it("round-trips a picked local time back to itself (timezone-independent)", () => {
    const local = "2026-06-11T09:00";
    expect(rfc3339ToDatetimeLocal(datetimeLocalToRFC3339(local))).toBe(local);
  });
});

describe("interval parts", () => {
  it("decomposes to the largest clean unit", () => {
    expect(parseIntervalToParts("30m")).toEqual({ value: 30, unit: "m" });
    expect(parseIntervalToParts("2h")).toEqual({ value: 2, unit: "h" });
    expect(parseIntervalToParts("48h")).toEqual({ value: 2, unit: "d" });
    expect(parseIntervalToParts("90m")).toEqual({ value: 90, unit: "m" }); // not a whole hour
  });

  it("preserves sub-minute intervals as seconds (round-trip, no silent rounding)", () => {
    expect(parseIntervalToParts("45s")).toEqual({ value: 45, unit: "s" }); // not a whole minute
    expect(parseIntervalToParts("90s")).toEqual({ value: 90, unit: "s" }); // 90s = 1.5 min, stays seconds
    expect(parseIntervalToParts("120s")).toEqual({ value: 2, unit: "m" }); // whole minutes → promoted
    expect(intervalPartsToString(45, "s")).toBe("45s");
  });

  it("collapses compound durations to equivalent minutes", () => {
    expect(parseIntervalToParts("2h30m")).toEqual({ value: 150, unit: "m" });
  });

  it("falls back to 30m for empty/unparseable input", () => {
    expect(parseIntervalToParts("")).toEqual({ value: 30, unit: "m" });
    expect(parseIntervalToParts("garbage")).toEqual({ value: 30, unit: "m" });
    expect(parseIntervalToParts("*/5 * * * *")).toEqual({ value: 30, unit: "m" });
  });

  it("composes a Go duration string, emitting days as hours", () => {
    expect(intervalPartsToString(30, "m")).toBe("30m");
    expect(intervalPartsToString(2, "h")).toBe("2h");
    expect(intervalPartsToString(1, "d")).toBe("24h");
    expect(intervalPartsToString(0, "m")).toBe("1m"); // clamps to >= 1
  });

  it("round-trips parse → compose for each unit", () => {
    for (const dur of ["5m", "1h", "24h", "72h"]) {
      const { value, unit } = parseIntervalToParts(dur);
      expect(parseIntervalToParts(intervalPartsToString(value, unit))).toEqual({ value, unit });
    }
  });
});
