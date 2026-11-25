import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getReviewStats, type ReviewStats } from "../../api/review";
import { darkColors } from "../../theme";
import { ReviewStatsView } from "./ReviewStatsView";

vi.mock("../../api/review", () => ({
  getReviewStats: vi.fn(),
}));

const statsMock = vi.mocked(getReviewStats);

const group = (key: string) => ({
  key,
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
  nice_to_have: 2,
  resolved: 2,
  outdated: 0,
  false_positive_rate: 0.2,
});

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
    by_model: [group("opus"), group("")],
    by_effort: [],
    by_scope: null,
    by_category: [group("security")],
    ...extra,
  };
}

const deferred = <T,>() => {
  let resolve: (v: T) => void = () => {};
  let reject: (e: unknown) => void = () => {};
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

describe("ReviewStatsView", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    statsMock.mockResolvedValue(stats());
  });

  it("loads the PR's stats over 90 days and renders cards and tables", async () => {
    render(<ReviewStatsView channelId="ch1" prNumber={7} colors={darkColors} />);
    expect(statsMock).toHaveBeenCalledWith("ch1", { days: 90, pr: 7 });
    expect(await screen.findByText("3m12s")).toBeTruthy();
    expect(screen.getByText("3m12s")).toBeTruthy();
    expect(screen.getByText("14%")).toBeTruthy();
    const target = screen.getByTestId("review-stats-target") as HTMLSelectElement;
    expect(target.value).toBe("pr");
    expect(within(target).getByText("PR #7")).toBeTruthy();

    const tables = screen.getAllByRole("table");
    expect(tables).toHaveLength(2);
    const model = tables[0];
    expect(within(model).getByText("Model")).toBeTruthy();
    expect(within(model).getByText("opus")).toBeTruthy();
    expect(within(model).getByText("(unknown)")).toBeTruthy();
    // Model rows show run counts; Category rows don't.
    expect(within(within(model).getAllByRole("row")[1]).getAllByRole("cell")[1].textContent).toBe("3");
    const category = tables[1];
    expect(within(category).getByText("security")).toBeTruthy();
    expect(within(within(category).getAllByRole("row")[1]).getAllByRole("cell")[1].textContent).toBe("-");
    expect(within(category).getByText("20%")).toBeTruthy();
  });

  it("covers the whole repo without a PR, and switches window and target", async () => {
    statsMock.mockResolvedValue(stats({ by_model: null, by_effort: null, by_scope: [group("full")], by_category: null }));
    const { unmount } = render(<ReviewStatsView channelId="ch1" colors={darkColors} />);
    expect(statsMock).toHaveBeenCalledWith("ch1", { days: 90, pr: undefined });
    const target = screen.getByTestId("review-stats-target") as HTMLSelectElement;
    expect(target.value).toBe("repo");
    expect(within(target).queryByText(/PR #/)).toBeNull();
    await userEvent.selectOptions(screen.getByTestId("review-stats-days"), "30");
    expect(statsMock).toHaveBeenLastCalledWith("ch1", { days: 30, pr: undefined });
    // Only the non-empty Scope table shows.
    await waitFor(() => expect(screen.getAllByRole("table")).toHaveLength(1));
    expect(screen.getByText("full")).toBeTruthy();
    unmount();

    render(<ReviewStatsView channelId="ch1" prNumber={7} colors={darkColors} />);
    await userEvent.selectOptions(screen.getByTestId("review-stats-target"), "repo");
    expect(statsMock).toHaveBeenLastCalledWith("ch1", { days: 90, pr: undefined });
    await userEvent.selectOptions(screen.getByTestId("review-stats-target"), "pr");
    expect(statsMock).toHaveBeenLastCalledWith("ch1", { days: 90, pr: 7 });
  });

  it("says when the window has no runs", async () => {
    statsMock.mockResolvedValue(stats({ runs: { ...stats().runs, total: 0 } }));
    render(<ReviewStatsView channelId="ch1" colors={darkColors} />);
    expect(await screen.findByText("No review runs in this window.")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("shows an error, as a message or a plain value, and clears it on reload", async () => {
    statsMock.mockRejectedValueOnce(new Error("forbidden"));
    render(<ReviewStatsView channelId="ch1" colors={darkColors} />);
    expect((await screen.findByTestId("review-stats-error")).textContent).toBe("forbidden");
    statsMock.mockRejectedValueOnce("offline");
    await userEvent.selectOptions(screen.getByTestId("review-stats-days"), "7");
    await waitFor(() => expect(screen.getByTestId("review-stats-error").textContent).toBe("offline"));
    await userEvent.selectOptions(screen.getByTestId("review-stats-days"), "365");
    expect(await screen.findByText("3m12s")).toBeTruthy();
    expect(screen.queryByTestId("review-stats-error")).toBeNull();
  });

  it("ignores results that land after the inputs changed or it unmounted", async () => {
    const first = deferred<ReviewStats>();
    const second = deferred<ReviewStats>();
    statsMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const { unmount } = render(<ReviewStatsView channelId="ch1" colors={darkColors} />);
    await userEvent.selectOptions(screen.getByTestId("review-stats-days"), "7");
    first.resolve(stats({ runs: { ...stats().runs, total: 0 } }));
    await Promise.resolve();
    expect(screen.queryByText("No review runs in this window.")).toBeNull();
    unmount();
    second.reject(new Error("late"));
    await Promise.resolve();
    expect(screen.queryByTestId("review-stats-error")).toBeNull();
  });
});
