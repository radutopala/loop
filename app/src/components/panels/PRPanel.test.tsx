import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cancelMerge, fixPR, getPRCheckLog, mergePR, type PRStatus, rerunPRChecks, setPRAutoFix } from "../../api/pr";
import { darkColors } from "../../theme";
import { PRPanel, prToneColor } from "./PRPanel";

vi.mock("../../api/pr", () => ({
  cancelMerge: vi.fn(),
  fixPR: vi.fn(),
  getPRCheckLog: vi.fn(),
  mergePR: vi.fn(),
  rerunPRChecks: vi.fn(),
  setPRAutoFix: vi.fn(),
}));

const status = (over: Partial<PRStatus> = {}): PRStatus => ({
  channel_id: "ch1",
  kind: "pr",
  number: 42,
  url: "https://forge.example/pr/42",
  head_sha: "",
  checks: { total: 0, passed: 0, failed: 0, pending: 0 },
  merge_state: "clean",
  auto_merge: false,
  pr_attention: "",
  auto_fix: false,
  auto_fix_default: false,
  attempts: 0,
  max_attempts: 3,
  ...over,
});

const failing = (over: Partial<PRStatus> = {}) =>
  status({
    checks: { total: 5, passed: 2, failed: 1, pending: 1 },
    failed: [
      { id: "c1", name: "unit", group: "ci", url: "https://forge.example/c1" },
      { id: "c2", name: "e2e", infra: true },
    ],
    ...over,
  });

const renderPanel = (s: PRStatus, onChanged = vi.fn()) => {
  const utils = render(<PRPanel channelId="ch1" status={s} colors={darkColors} onChanged={onChanged} />);
  return { ...utils, onChanged };
};

function rgb(color: string): string {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
}

describe("prToneColor", () => {
  it("maps each tone to its color", () => {
    expect(prToneColor("ok", darkColors)).toBe("#3fb950");
    expect(prToneColor("failed", darkColors)).toBe(darkColors.error);
    expect(prToneColor("running", darkColors)).toBe(darkColors.warning);
    expect(prToneColor("merging", darkColors)).toBe(darkColors.active);
    expect(prToneColor("merged", darkColors)).toBe("#a371f7");
    expect(prToneColor("closed", darkColors)).toBe(darkColors.textDim);
  });
});

describe("PRPanel", () => {
  let openSpy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    vi.resetAllMocks();
    openSpy = vi.spyOn(window, "open").mockReturnValue(null);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows the chip and opens the PR on click", async () => {
    renderPanel(status());
    const chip = screen.getByTestId("git-panel-pr-chip");
    expect(chip.textContent).toBe("PR #42 · no checks");
    expect(chip.getAttribute("data-tone")).toBe("ok");
    await userEvent.click(chip);
    expect(openSpy).toHaveBeenCalledWith("https://forge.example/pr/42", "_blank", "noopener,noreferrer");
    // No check counts, head, threads, stale, conflict, reason or attempts.
    expect(screen.queryByText(/passed/)).toBeNull();
    expect(screen.queryByText(/^head/)).toBeNull();
    expect(screen.queryByTestId("git-panel-pr-threads")).toBeNull();
    expect(screen.queryByTestId("git-panel-pr-stale")).toBeNull();
    expect(screen.queryByTestId("git-panel-pr-conflict")).toBeNull();
    expect(screen.queryByTestId("git-panel-pr-reason")).toBeNull();
    expect(screen.queryByText(/Failed checks/)).toBeNull();
  });

  it("does not open anything without a URL", async () => {
    renderPanel(status({ url: undefined }));
    await userEvent.click(screen.getByTestId("git-panel-pr-chip"));
    expect(openSpy).not.toHaveBeenCalled();
  });

  it("summarises checks, head, threads, staleness, reason and attempts", async () => {
    renderPanel(
      failing({
        checks: { total: 6, passed: 2, failed: 1, pending: 1 },
        head_sha: "abcdef1234",
        checkout_sha: "1234567890",
        unresolved_threads: 2,
        reason: "auto_fix_off",
        attempts: 1,
      }),
    );
    expect(screen.getByText("2/6 passed · 1 failed · 1 running · 2 skipped")).toBeTruthy();
    expect(screen.getByText("abcdef1")).toBeTruthy();
    expect(screen.getByTestId("git-panel-pr-threads").textContent).toBe("2 unresolved review threads");
    expect(screen.getByTestId("git-panel-pr-stale").textContent).toContain("1234567");
    expect(screen.getByTestId("git-panel-pr-reason").textContent).toBe("Auto-fix is off for this thread.");
    expect(screen.getByText("Auto-fix attempts: 1/3")).toBeTruthy();
    await userEvent.click(screen.getByTestId("git-panel-pr-threads"));
    expect(openSpy).toHaveBeenCalledWith("https://forge.example/pr/42", "_blank", "noopener,noreferrer");
  });

  it("shows checks with nothing failed, running or skipped, and a single thread", () => {
    renderPanel(status({ checks: { total: 2, passed: 2, failed: 0, pending: 0 }, unresolved_threads: 1 }));
    expect(screen.getByText("2/2 passed")).toBeTruthy();
    expect(screen.getByTestId("git-panel-pr-threads").textContent).toBe("1 unresolved review thread");
  });

  it("shows a merge conflict", () => {
    renderPanel(status({ merge_state: "conflict", base_ref: "main" }));
    expect(screen.getByTestId("git-panel-pr-conflict").textContent).toBe("Merge conflicts with main.");
  });

  it("hides the actions once merged", () => {
    renderPanel(status({ merged: true }));
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("toggles auto-fix and refetches", async () => {
    let resolve: () => void = () => {};
    vi.mocked(setPRAutoFix).mockReturnValue(new Promise<void>((r) => (resolve = r)));
    const { onChanged } = renderPanel(status());
    const box = screen.getByRole("checkbox") as HTMLInputElement;
    expect(screen.getByText("Auto-fix failed checks: off")).toBeTruthy();
    await userEvent.click(box);
    expect(setPRAutoFix).toHaveBeenCalledWith("ch1", true);
    // Busy: controls disable while the action runs.
    expect(box.disabled).toBe(true);
    await act(async () => resolve());
    expect(onChanged).toHaveBeenCalledTimes(1);
    expect(box.disabled).toBe(false);
  });

  it("offers Fix it and Re-run failed for failures", async () => {
    vi.mocked(fixPR).mockResolvedValue();
    vi.mocked(rerunPRChecks).mockResolvedValue();
    const { onChanged } = renderPanel(failing());
    await userEvent.click(screen.getByRole("button", { name: "Fix it" }));
    expect(fixPR).toHaveBeenCalledWith("ch1");
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    await userEvent.click(screen.getByRole("button", { name: "Re-run failed" }));
    expect(rerunPRChecks).toHaveBeenCalledWith("ch1");
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2));
  });

  it("merges when ready and now, with the merge method", async () => {
    vi.mocked(mergePR).mockResolvedValue();
    const { onChanged } = renderPanel(status({ merge_method: "squash" }));
    await userEvent.click(screen.getByRole("button", { name: "Merge when ready (squash)" }));
    expect(mergePR).toHaveBeenCalledWith("ch1", true);
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    await userEvent.click(screen.getByRole("button", { name: "Merge now (squash)" }));
    expect(mergePR).toHaveBeenCalledWith("ch1", false);
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(2));
  });

  it("cancels an armed merge, labelled without a method when there is none", async () => {
    vi.mocked(cancelMerge).mockResolvedValue();
    const { onChanged, unmount } = renderPanel(status({ merge_armed: "rebase" }));
    expect(screen.getByRole("button", { name: "Cancel auto-merge (rebase)" })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Cancel auto-merge (rebase)" }));
    expect(cancelMerge).toHaveBeenCalledWith("ch1");
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    unmount();
    renderPanel(status());
    expect(screen.getByRole("button", { name: "Merge when ready" })).toBeTruthy();
  });

  it("shows an action's error, as a message or a plain value", async () => {
    vi.mocked(fixPR).mockRejectedValue(new Error("forbidden"));
    vi.mocked(rerunPRChecks).mockRejectedValue("offline");
    const { onChanged } = renderPanel(failing());
    await userEvent.click(screen.getByRole("button", { name: "Fix it" }));
    expect(await screen.findByText("forbidden")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Re-run failed" }));
    expect(await screen.findByText("offline")).toBeTruthy();
    expect(screen.queryByText("forbidden")).toBeNull();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("lists failed checks with links, runner marks and logs on demand", async () => {
    let resolve: (t: string) => void = () => {};
    vi.mocked(getPRCheckLog).mockReturnValueOnce(new Promise((r) => (resolve = r)));
    renderPanel(failing());
    expect(screen.getByText("Failed checks")).toBeTruthy();
    const unit = screen.getByText("ci / unit");
    await userEvent.click(unit);
    expect(openSpy).toHaveBeenCalledWith("https://forge.example/c1", "_blank", "noopener,noreferrer");
    expect(screen.getByText("e2e")).toBeTruthy();
    expect(screen.getByText("(runner)")).toBeTruthy();
    // A check without a URL opens nothing.
    await userEvent.click(screen.getByText("e2e"));
    expect(openSpy).toHaveBeenCalledTimes(1);

    const toggle = screen.getAllByTestId("git-panel-pr-log-toggle")[0]!;
    await userEvent.click(toggle);
    expect(getPRCheckLog).toHaveBeenCalledWith("ch1", "c1");
    expect(screen.getByTestId("git-panel-pr-log").textContent).toBe("Loading…");
    expect(toggle.textContent).toBe("Hide log");
    await act(async () => resolve("line 1\nline 2"));
    expect(screen.getByTestId("git-panel-pr-log").textContent).toBe("line 1\nline 2");
    expect(screen.getByTestId("git-panel-pr-log").style.color).toBe(rgb(darkColors.textLight));
    await userEvent.click(toggle);
    expect(screen.queryByTestId("git-panel-pr-log")).toBeNull();
    expect(toggle.textContent).toBe("Log");
  });

  it("shows an empty log and log errors", async () => {
    vi.mocked(getPRCheckLog).mockResolvedValueOnce("").mockRejectedValueOnce(new Error("gone"));
    const { unmount } = renderPanel(failing());
    const [t1, t2] = screen.getAllByTestId("git-panel-pr-log-toggle") as [HTMLElement, HTMLElement];
    await userEvent.click(t1);
    await waitFor(() => expect(screen.getByTestId("git-panel-pr-log").textContent).toBe("(empty log)"));
    await userEvent.click(t2);
    await waitFor(() => expect(screen.getAllByTestId("git-panel-pr-log")[1]!.textContent).toBe("gone"));
    expect(screen.getAllByTestId("git-panel-pr-log")[1]!.style.color).toBe(rgb(darkColors.error));
    unmount();

    vi.mocked(getPRCheckLog).mockRejectedValueOnce("denied");
    renderPanel(failing());
    fireEvent.click(screen.getAllByTestId("git-panel-pr-log-toggle")[0]!);
    await waitFor(() => expect(screen.getByTestId("git-panel-pr-log").textContent).toBe("denied"));
  });
});
