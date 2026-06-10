import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { timeAgo, nextRunLabel, TYPE_COLORS } from "./taskUtils";

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
    expect(Object.keys(TYPE_COLORS).sort()).toEqual(["cron", "interval", "once"]);
  });
});
