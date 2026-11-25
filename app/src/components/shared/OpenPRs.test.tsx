import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchOpenPRs, type PRInfo } from "../../api/git";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { OpenPRRows, PickerSection, useOpenPRs } from "./OpenPRs";

vi.mock("../../api/git", () => ({ fetchOpenPRs: vi.fn() }));

const fetchMock = vi.mocked(fetchOpenPRs);

const pr: PRInfo = { number: 12, url: "u", base_ref: "main", head_ref: "feat/login", state: "OPEN", title: "Fix the login" };
const draft: PRInfo = { number: 13, url: "u", base_ref: "main", head_ref: "feat/untitled", state: "OPEN", is_draft: true };

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useOpenPRs", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does nothing while disabled", () => {
    const { result } = renderHook(() => useOpenPRs("ch-1", false));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current).toEqual({ prs: null, error: null, forge: undefined });
  });

  it("loads the PRs and forge, reloading from scratch when re-enabled", async () => {
    fetchMock.mockResolvedValueOnce({ prs: [pr], forge: "github" });
    const { result, rerender } = renderHook(({ enabled }) => useOpenPRs("ch-1", enabled), { initialProps: { enabled: true } });
    expect(result.current.prs).toBeNull();
    await waitFor(() => expect(result.current).toEqual({ prs: [pr], error: null, forge: "github" }));
    expect(fetchMock).toHaveBeenCalledWith("ch-1");

    const next = deferred<{ prs: PRInfo[]; forge?: string }>();
    fetchMock.mockReturnValueOnce(next.promise);
    rerender({ enabled: false });
    rerender({ enabled: true });
    expect(result.current).toEqual({ prs: null, error: null, forge: undefined });
    await act(async () => next.resolve({ prs: [] }));
    expect(result.current).toEqual({ prs: [], error: null, forge: undefined });
  });

  it.each([
    ["an Error's trimmed message", new Error("  gh: not logged in \n"), "gh: not logged in"],
    ["anything else as a string", "boom", "boom"],
  ])("reports a failed listing with %s", async (_name, err, want) => {
    fetchMock.mockRejectedValueOnce(err);
    const { result } = renderHook(() => useOpenPRs("ch-1", true));
    await waitFor(() => expect(result.current.error).toBe(want));
    expect(result.current.prs).toEqual([]);
  });

  it("drops a stale listing after the channel changes", async () => {
    const first = deferred<{ prs: PRInfo[] }>();
    const firstFail = deferred<{ prs: PRInfo[] }>();
    fetchMock
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(firstFail.promise)
      .mockResolvedValueOnce({ prs: [draft], forge: "gitlab" });
    const { result, rerender } = renderHook(({ id }) => useOpenPRs(id, true), { initialProps: { id: "a" } });
    rerender({ id: "b" });
    rerender({ id: "c" });
    await waitFor(() => expect(result.current.prs).toEqual([draft]));
    await act(async () => {
      first.resolve({ prs: [pr] });
      firstFail.reject(new Error("late"));
    });
    expect(result.current).toEqual({ prs: [draft], error: null, forge: "gitlab" });
  });
});

describe("PickerSection", () => {
  it("draws a title band and divider when asked", () => {
    render(
      <PickerSection testId="sec" title="Branches" divided>
        <span>row</span>
      </PickerSection>,
    );
    const sec = screen.getByTestId("sec");
    expect(sec.style.borderTop).toContain("1px solid");
    expect(screen.getByText("Branches")).toBeTruthy();
    expect((screen.getByText("row").parentElement as HTMLElement).style.padding).toBe("2px 0px 4px");
  });

  it("is just the scroll area without a title", () => {
    render(
      <PickerSection testId="sec">
        <span>row</span>
      </PickerSection>,
    );
    expect(screen.getByTestId("sec").style.borderTop).toBe("");
    expect(screen.getByTestId("sec").children).toHaveLength(1);
    expect((screen.getByText("row").parentElement as HTMLElement).style.padding).toBe("4px 0px");
  });
});

describe("OpenPRRows", () => {
  const base = { error: null, search: "", worktrees: undefined, testId: "prs" };

  it("renders nothing for a repo without a forge", () => {
    const { container } = render(<OpenPRRows {...base} prs={[]} onPick={vi.fn()} />);
    expect(container.innerHTML).toBe("");
  });

  it("shows loading under a pull-request title", () => {
    render(<OpenPRRows {...base} prs={null} onPick={vi.fn()} />);
    expect(screen.getByText("Pull requests")).toBeTruthy();
    expect(screen.getByText("Loading…")).toBeTruthy();
  });

  it("shows a failed listing's error", () => {
    render(<OpenPRRows {...base} prs={[]} error="gh: not logged in" onPick={vi.fn()} />);
    expect(screen.getByText("gh: not logged in")).toBeTruthy();
    expect(screen.queryByText("No open pull requests")).toBeNull();
  });

  it.each([
    ["a GitLab repo with none open", [], "gitlab", "", "Merge requests", "No open merge requests"],
    ["a search matching nothing", [pr], "github", "zzz", "Pull requests", "No matching PRs"],
  ])("notes %s", (_name, prs, forge, search, title, note) => {
    render(<OpenPRRows {...base} prs={prs} forge={forge} search={search} onPick={vi.fn()} />);
    expect(screen.getByText(title)).toBeTruthy();
    expect(screen.getByText(note)).toBeTruthy();
  });

  it("lists PRs with their action, and picks one with its worktree", () => {
    const onPick = vi.fn();
    const onThread = { path: "/r/.worktrees/login", branch: "feat/login", thread_id: "t1" };
    render(<OpenPRRows {...base} prs={[pr, draft]} worktrees={[onThread]} onPick={onPick} />);

    const login = screen.getByTestId("prs-12");
    expect(login.textContent).toBe("#12Fix the loginfeat/logingo");
    expect(login.title).toBe("Go to the worktree thread on feat/login");
    expect((login.lastElementChild as HTMLElement).style.color).toBe(css(darkColors.active));

    const untitled = screen.getByTestId("prs-13");
    expect(untitled.textContent).toBe("#13draft · feat/untitledfeat/untitled+wt");
    expect((untitled.lastElementChild as HTMLElement).style.color).toBe(css(darkColors.textDim));

    fireEvent.mouseEnter(login);
    expect(login.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
    fireEvent.mouseLeave(login);
    expect(login.style.backgroundColor).toBe("transparent");

    fireEvent.click(login);
    expect(onPick).toHaveBeenCalledWith(pr, onThread);
    fireEvent.click(untitled);
    expect(onPick).toHaveBeenLastCalledWith(draft, undefined);
  });

  it("titles a list of MRs as merge requests", () => {
    render(<OpenPRRows {...base} prs={[{ ...pr, kind: "mr" }]} onPick={vi.fn()} />);
    expect(screen.getByText("Merge requests")).toBeTruthy();
    expect(screen.getByText("!12")).toBeTruthy();
  });
});
