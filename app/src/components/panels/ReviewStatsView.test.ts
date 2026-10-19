import { describe, expect, it } from "vitest";
import type { ReviewStats } from "../../api/review";
import { formatStatsDuration, formatStatsRate, reviewStatsCards, reviewStatsTables } from "./ReviewStatsView";

function stats(extra: Partial<ReviewStats> = {}): ReviewStats {
  return {
    runs: { total: 4, completed: 3, failed: 1, cancelled: 0, running: 0, full: 3, incremental: 1, prs: 1, median_ms: 192400, p90_ms: 0 },
    findings: {
      added: 10,
      folded: 2,
      deleted: 1,
      pushed: 4,
      real: 5,
      false_positive: 1,
      already_fixed: 1,
      unjudged: 3,
      blocking: 2,
      nice_to_have: 3,
      resolved: 2,
      outdated: 1,
      false_positive_rate: 0.1428,
    },
    by_model: null,
    by_effort: [],
    by_scope: null,
    by_category: null,
    ...extra,
  };
}

const group = {
  key: "opus",
  runs: 3,
  added: 8,
  folded: 0,
  deleted: 0,
  pushed: 3,
  real: 4,
  false_positive: 1,
  already_fixed: 0,
  unjudged: 0,
  blocking: 1,
  nice_to_have: 3,
  resolved: 2,
  outdated: 0,
  false_positive_rate: 0.2,
};

describe("formatStatsRate", () => {
  it("rounds to a whole percent", () => {
    expect(formatStatsRate(0.1428)).toBe("14%");
    expect(formatStatsRate(0)).toBe("0%");
    expect(formatStatsRate(1)).toBe("100%");
  });
});

describe("formatStatsDuration", () => {
  it.each([
    [0, "-"],
    [400, "0s"],
    [42_000, "42s"],
    [192_400, "3m12s"],
    [3_723_000, "1h2m3s"],
  ])("%d ms is %s", (ms, want) => {
    expect(formatStatsDuration(ms)).toBe(want);
  });
});

describe("reviewStatsCards", () => {
  it("sums runs and findings up", () => {
    expect(reviewStatsCards(stats())).toEqual([
      { label: "Runs", value: "4", detail: "3 completed, 1 failed, 0 cancelled · 1 PR" },
      { label: "Scope", value: "1/4", detail: "incremental · 3 full" },
      { label: "Duration", value: "3m12s", detail: "median · p90 -" },
      { label: "Findings", value: "10", detail: "2 folded, 1 deleted, 4 pushed" },
      { label: "False positives", value: "14%", detail: "5 real, 1 false, 1 already fixed, 3 unjudged" },
      { label: "Blocking", value: "2", detail: "of 5 real · 3 nice to have" },
      { label: "Pushed", value: "4", detail: "2 resolved, 1 outdated" },
    ]);
  });

  it("pluralises PRs", () => {
    expect(reviewStatsCards(stats({ runs: { ...stats().runs, prs: 2 } }))[0]!.detail).toBe("3 completed, 1 failed, 0 cancelled · 2 PRs");
  });
});

describe("reviewStatsTables", () => {
  it("skips empty breakdowns and counts no runs per category", () => {
    const st = stats({ by_model: [group], by_category: [{ ...group, key: "bug" }] });
    expect(reviewStatsTables(st)).toEqual([
      { title: "Model", runs: true, rows: [group] },
      { title: "Category", runs: false, rows: [{ ...group, key: "bug" }] },
    ]);
  });

  it("is empty without breakdowns", () => {
    expect(reviewStatsTables(stats())).toEqual([]);
  });
});
