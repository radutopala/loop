import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  type ConfigHistory as ConfigHistoryData,
  type ConfigHistoryFiles,
  type ConfigRevision,
  type ConfigRevisionSummary,
  fetchConfigHistory,
  fetchConfigHistoryFiles,
  fetchConfigRevision,
  restoreConfigRevision,
} from "../../api/configApi";
import { darkColors } from "../../theme";
import { ConfigHistory } from "./ConfigHistory";

vi.mock("../../api/configApi", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/configApi")>();
  return {
    ...actual,
    fetchConfigHistory: vi.fn(),
    fetchConfigHistoryFiles: vi.fn(),
    fetchConfigRevision: vi.fn(),
    restoreConfigRevision: vi.fn(),
  };
});

const historyMock = vi.mocked(fetchConfigHistory);
const filesMock = vi.mocked(fetchConfigHistoryFiles);
const revisionMock = vi.mocked(fetchConfigRevision);
const restoreMock = vi.mocked(restoreConfigRevision);

const revs: ConfigRevisionSummary[] = [
  { id: 3, source: "settings", created_at: "2026-01-03T00:00:00Z", added: 2, removed: 1 },
  { id: 2, source: "restore:1", created_at: "2026-01-02T00:00:00Z", added: 1, removed: 0 },
  { id: 1, source: "initial", created_at: "2026-01-01T00:00:00Z", added: 5, removed: 0 },
];

function revision(id: number, diff = `+line ${id}`): ConfigRevision {
  return { id, path: "/cfg/config.json", content: "{}", hash: "h", source: "settings", created_at: "2026-01-01T00:00:00Z", diff };
}

function history(over: Partial<ConfigHistoryData> = {}): ConfigHistoryData {
  return { path: "/cfg/config.json", revisions: revs, ...over };
}

function files(...names: string[]): ConfigHistoryFiles {
  return { root: "/cfg", files: names.map((file) => ({ file, revisions: 1, updated_at: "" })) };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

type Props = Parameters<typeof ConfigHistory>[0];

function renderHistory(props: Partial<Props> = {}) {
  const onRestored = vi.fn();
  const user = userEvent.setup();
  const all: Props = { colors: darkColors, refreshKey: 0, dirty: false, onRestored, ...props };
  const utils = render(<ConfigHistory {...all} />);
  const rerender = (next: Partial<Props>) => utils.rerender(<ConfigHistory {...all} {...next} />);
  return { onRestored, user, rerender };
}

const items = () => screen.getAllByTestId("config-history-item");

describe("ConfigHistory", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    historyMock.mockResolvedValue(history());
    filesMock.mockResolvedValue(files("config.json", "templates/a.md"));
    revisionMock.mockImplementation(async (id) => revision(id));
    restoreMock.mockResolvedValue(undefined);
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("lists the global config's revisions, marking the current one", async () => {
    renderHistory();
    expect(screen.getByText("Global Config History")).toBeTruthy();
    expect(screen.getByText("Loading...")).toBeTruthy();
    await waitFor(() => expect(items()).toHaveLength(3));
    expect(historyMock).toHaveBeenCalledWith(undefined, undefined);
    expect(filesMock).toHaveBeenCalledWith(undefined);
    expect(items()[0]!.textContent).toContain("Settings · current");
    expect(items()[1]!.textContent).toContain("Restored #1");
    expect(items()[1]!.textContent).toContain("+1");
    expect(items()[2]!.textContent).toContain("First seen");
    expect(screen.getByText("/cfg/config.json")).toBeTruthy();
    expect(screen.getByText("Select a revision to see what it changed.")).toBeTruthy();
    const options = [...screen.getByTestId("config-history-file").querySelectorAll("option")].map((o) => o.value);
    expect(options).toEqual(["config.json", "templates/a.md"]);
  });

  it("titles a project's history and fetches it by channel", async () => {
    renderHistory({ channelId: "ch-1" });
    expect(screen.getByText("Project Config History")).toBeTruthy();
    await waitFor(() => expect(items()).toHaveLength(3));
    expect(historyMock).toHaveBeenCalledWith("ch-1", undefined);
    expect(filesMock).toHaveBeenCalledWith("ch-1");
  });

  it("says there is no history yet, showing the file name until a path is known", async () => {
    historyMock.mockResolvedValue({ path: "", revisions: [] });
    renderHistory();
    await waitFor(() => expect(screen.getByText("No history yet.")).toBeTruthy());
    expect(document.querySelector("code")?.textContent).toBe("config.json");
  });

  it("shows no history when the history fetch fails", async () => {
    historyMock.mockRejectedValue(new Error("down"));
    filesMock.mockRejectedValue(new Error("down"));
    renderHistory();
    await waitFor(() => expect(screen.getByText("No history yet.")).toBeTruthy());
    expect(console.warn).toHaveBeenCalledWith("[loop] fetching config history:", expect.any(Error));
    expect(console.warn).toHaveBeenCalledWith("[loop] fetching config history files:", expect.any(Error));
  });

  it("marks a deleted file and lets its newest revision be restored", async () => {
    historyMock.mockResolvedValue(history({ missing: true }));
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    expect(screen.getByTestId("config-history-missing").textContent).toContain("(deleted)");
    expect(items()[0]!.textContent).not.toContain("current");
    await user.click(items()[0]!);
    await waitFor(() => expect(screen.getByTestId("config-history-restore")).toHaveProperty("disabled", false));
  });

  it("shows a selected revision's diff from the one before it", async () => {
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    expect(revisionMock).toHaveBeenCalledWith(2, undefined);
    await waitFor(() => expect(screen.getByTestId("config-history-diff").textContent).toContain("+line 2"));
    expect(screen.getByTestId("config-history-diff-caption").textContent).toBe("Changes from #1 to #2");
    const restore = screen.getByTestId("config-history-restore");
    expect(restore).toHaveProperty("disabled", false);
    expect(restore.getAttribute("title")).toBeNull();
    const against = [...screen.getByTestId("config-history-against").querySelectorAll("option")].map((o) => o.textContent);
    expect(against[0]!).toBe("The revision before it");
    expect(against).toHaveLength(3);
    expect(against[1]!).toContain("#3 · Settings (current) · ");
    expect(against[2]!).toContain("#1 · First seen · ");
  });

  it("shows the oldest revision in full", async () => {
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[2]!);
    await waitFor(() => expect(screen.getByTestId("config-history-diff-caption").textContent).toBe("#1, the oldest revision, in full"));
  });

  it("can't restore the current revision", async () => {
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[0]!);
    const restore = await screen.findByTestId("config-history-restore");
    expect(restore).toHaveProperty("disabled", true);
    expect(restore.getAttribute("title")).toBe("This is the current version");
  });

  it("can't restore the config over unsaved edits, but can restore another file", async () => {
    historyMock.mockImplementation(async (_ch, file) => history({ path: file ? `/cfg/${file}` : "/cfg/config.json" }));
    const { user } = renderHistory({ dirty: true });
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    const restore = await screen.findByTestId("config-history-restore");
    expect(restore).toHaveProperty("disabled", true);
    expect(restore.getAttribute("title")).toBe("Save or cancel your edits first");

    await user.selectOptions(screen.getByTestId("config-history-file"), "templates/a.md");
    expect(historyMock).toHaveBeenLastCalledWith(undefined, "templates/a.md");
    await waitFor(() => expect(screen.getByText("/cfg/templates/a.md")).toBeTruthy());
    await user.click(items()[1]!);
    await waitFor(() => expect(screen.getByTestId("config-history-restore")).toHaveProperty("disabled", false));
  });

  it("diffs against a chosen revision and back to the one before it", async () => {
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[0]!);
    const select = await screen.findByTestId("config-history-against");
    await user.selectOptions(select, "1");
    expect(revisionMock).toHaveBeenLastCalledWith(3, 1);
    await waitFor(() => expect(screen.getByTestId("config-history-diff-caption").textContent).toBe("Changes from #1 to #3"));

    // Another revision keeps the chosen one to diff against.
    await user.click(items()[1]!);
    expect(revisionMock).toHaveBeenLastCalledWith(2, 1);
    // Picking the against revision itself drops the comparison.
    await user.click(items()[2]!);
    expect(revisionMock).toHaveBeenLastCalledWith(1, undefined);

    await user.click(items()[0]!);
    await user.selectOptions(screen.getByTestId("config-history-against"), "2");
    expect(revisionMock).toHaveBeenLastCalledWith(3, 2);
    await user.selectOptions(screen.getByTestId("config-history-against"), "");
    expect(revisionMock).toHaveBeenLastCalledWith(3, undefined);
  });

  it("shows a revision that failed to load, with a fallback message", async () => {
    revisionMock.mockRejectedValueOnce(new Error("gone")).mockRejectedValueOnce({});
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    await waitFor(() => expect(screen.getByText("gone")).toBeTruthy());
    await user.click(items()[2]!);
    await waitFor(() => expect(screen.getByText("Failed to load the revision")).toBeTruthy());
  });

  it("keeps the latest revision when an earlier fetch finishes last", async () => {
    const slow = deferred<ConfigRevision>();
    const slowFail = deferred<ConfigRevision>();
    revisionMock.mockReturnValueOnce(slow.promise).mockReturnValueOnce(slowFail.promise);
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    await user.click(items()[2]!);
    await user.click(items()[0]!);
    await waitFor(() => expect(screen.getByTestId("config-history-diff").textContent).toContain("+line 3"));
    await act(async () => {
      slow.resolve(revision(2));
      slowFail.reject(new Error("late"));
    });
    expect(screen.getByTestId("config-history-diff").textContent).toContain("+line 3");
    expect(screen.queryByText("late")).toBeNull();
  });

  it("restores the config, reloads the history and tells the parent", async () => {
    const { user, onRestored } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    await user.click(await screen.findByTestId("config-history-restore"));
    expect(restoreMock).toHaveBeenCalledWith(2);
    await waitFor(() => expect(onRestored).toHaveBeenCalledOnce());
    expect(historyMock).toHaveBeenCalledTimes(2);
    expect(screen.getByText("Select a revision to see what it changed.")).toBeTruthy();
  });

  it("restores another file without asking the parent to re-fetch the config", async () => {
    const { user, onRestored } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.selectOptions(screen.getByTestId("config-history-file"), "templates/a.md");
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    await user.click(await screen.findByTestId("config-history-restore"));
    await waitFor(() => expect(historyMock).toHaveBeenCalledTimes(3));
    expect(restoreMock).toHaveBeenCalledWith(2);
    expect(onRestored).not.toHaveBeenCalled();
  });

  it("disables restore while it runs and shows a failure", async () => {
    const pending = deferred<void>();
    restoreMock.mockReturnValueOnce(pending.promise).mockRejectedValueOnce({});
    const { user } = renderHistory();
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.click(items()[1]!);
    const restore = await screen.findByTestId("config-history-restore");
    await user.click(restore);
    expect(restore).toHaveProperty("disabled", true);
    await act(async () => pending.reject(new Error("locked")));
    expect(screen.getByText("locked")).toBeTruthy();
    expect(restore).toHaveProperty("disabled", false);
    await user.click(restore);
    await waitFor(() => expect(screen.getByText("Failed to restore")).toBeTruthy());
  });

  it("drops a history fetch that a newer one replaced", async () => {
    const first = deferred<ConfigHistoryData>();
    const firstFiles = deferred<ConfigHistoryFiles>();
    const failing = deferred<ConfigHistoryData>();
    historyMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(failing.promise);
    filesMock.mockReturnValueOnce(firstFiles.promise);
    const { rerender } = renderHistory();
    rerender({ refreshKey: 1 });
    rerender({ refreshKey: 2 });
    await waitFor(() => expect(items()).toHaveLength(3));
    await act(async () => {
      first.resolve(history({ path: "/old/config.json", revisions: [revs[2]!] }));
      firstFiles.resolve(files("stale.md"));
      failing.reject(new Error("late"));
    });
    expect(items()).toHaveLength(3);
    expect(screen.queryByText("/old/config.json")).toBeNull();
    expect(screen.queryByRole("option", { name: "stale.md" })).toBeNull();
  });

  it("goes back to the config file when the project changes", async () => {
    const { user, rerender } = renderHistory({ channelId: "ch-1" });
    await waitFor(() => expect(items()).toHaveLength(3));
    await user.selectOptions(screen.getByTestId("config-history-file"), "templates/a.md");
    expect(historyMock).toHaveBeenLastCalledWith("ch-1", "templates/a.md");
    rerender({ channelId: "ch-2" });
    await waitFor(() => expect(historyMock).toHaveBeenLastCalledWith("ch-2", undefined));
    expect((screen.getByTestId("config-history-file") as HTMLSelectElement).value).toBe("config.json");
  });
});
