import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { type CommitEntry, type DiffResponse, fetchCommitDiff } from "../../api/loopApi";
import { CommitDiffView } from "./CommitDiffView";

vi.mock("../../api/loopApi", () => ({
  fetchCommitDiff: vi.fn(),
}));

// DiffViewer lays out every hunk; the stub shows the props the view hands it
// and wires its buttons to the view's callbacks.
vi.mock("./DiffViewer", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./DiffViewer")>();
  return {
    ...actual,
    DiffViewer: (p: {
      files: { path: string }[];
      parsedFiles: unknown[];
      expandedFiles: Set<string>;
      loading: boolean;
      hasData: boolean;
      totalFiles: number;
      rootIndex: number;
      fileRef?: string;
      onToggleFile: (key: string) => void;
      onExpandAll: () => void;
      onCollapseAll: () => void;
      onFileContextMenu: (e: React.MouseEvent, path: string) => void;
    }) => (
      <div
        data-testid="diff-stub"
        data-loading={String(p.loading)}
        data-has-data={String(p.hasData)}
        data-total={p.totalFiles}
        data-parsed={p.parsedFiles.length}
        data-root={p.rootIndex}
        data-ref={p.fileRef}
        data-expanded={[...p.expandedFiles].sort().join(",")}
      >
        <button onClick={() => p.onToggleFile(":a.go")}>toggle a.go</button>
        <button onClick={p.onExpandAll}>expand all</button>
        <button onClick={p.onCollapseAll}>collapse all</button>
        <button onContextMenu={(e) => p.onFileContextMenu(e, "a.go")}>menu a.go</button>
      </div>
    ),
  };
});

const fetchMock = vi.mocked(fetchCommitDiff);

const commit: CommitEntry = { hash: "abcdef123456", short: "abcdef1", subject: "Add login", author: "Ann", date: "2026-01-02T10:00:00Z", body: "  Details here  " };

const diff: DiffResponse = {
  files: [
    { path: "a.go", additions: 2, deletions: 1, binary: false },
    { path: "b.go", additions: 1, deletions: 0, binary: false },
  ],
  diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,2 @@\n-x\n+y\n+z\n",
  total_additions: 3,
  total_deletions: 1,
};

function renderView(props: Partial<Parameters<typeof CommitDiffView>[0]> = {}) {
  const onBack = vi.fn();
  const onFileContextMenu = vi.fn();
  const utils = render(<CommitDiffView channelId="ch-1" commit={commit} rootIndex={2} onBack={onBack} onFileContextMenu={onFileContextMenu} {...props} />);
  return { ...utils, onBack, onFileContextMenu };
}

describe("CommitDiffView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("fetches the commit's diff once and starts with every file expanded", async () => {
    fetchMock.mockResolvedValue(diff);
    renderView();
    expect(fetchMock).toHaveBeenCalledWith("ch-1", "abcdef123456", 2);
    expect(screen.getByTestId("diff-stub").dataset.loading).toBe("true");

    await waitFor(() => expect(screen.getByTestId("diff-stub").dataset.loading).toBe("false"));
    const stub = screen.getByTestId("diff-stub");
    expect(stub.dataset.hasData).toBe("true");
    expect(stub.dataset.total).toBe("2");
    expect(stub.dataset.parsed).toBe("1");
    expect(stub.dataset.root).toBe("2");
    expect(stub.dataset.ref).toBe("abcdef123456");
    expect(stub.dataset.expanded).toBe(":a.go,:b.go");
    expect(screen.getByText("+3")).toBeTruthy();
    expect(screen.getByText("-1")).toBeTruthy();
    expect(screen.getByTestId("commit-diff-hash").textContent).toBe("abcdef1");
    expect(screen.getByTestId("commit-diff-hash").title).toBe("abcdef123456");
  });

  it("toggles, collapses and expands files", async () => {
    fetchMock.mockResolvedValue(diff);
    const user = userEvent.setup();
    renderView();
    await waitFor(() => expect(screen.getByTestId("diff-stub").dataset.expanded).toBe(":a.go,:b.go"));

    await user.click(screen.getByText("toggle a.go"));
    expect(screen.getByTestId("diff-stub").dataset.expanded).toBe(":b.go");
    await user.click(screen.getByText("toggle a.go"));
    expect(screen.getByTestId("diff-stub").dataset.expanded).toBe(":a.go,:b.go");
    await user.click(screen.getByText("collapse all"));
    expect(screen.getByTestId("diff-stub").dataset.expanded).toBe("");
    await user.click(screen.getByText("expand all"));
    expect(screen.getByTestId("diff-stub").dataset.expanded).toBe(":a.go,:b.go");
  });

  it("expands nothing when expand all is pressed before the diff arrives", async () => {
    fetchMock.mockReturnValue(new Promise(() => {}));
    const user = userEvent.setup();
    renderView();
    await user.click(screen.getByText("expand all"));
    expect(screen.getByTestId("diff-stub").dataset.expanded).toBe("");
  });

  it("shows the fetch error instead of the diff", async () => {
    fetchMock.mockRejectedValue(new Error("no such commit"));
    renderView();
    expect(await screen.findByText("no such commit")).toBeTruthy();
    expect(screen.queryByTestId("diff-stub")).toBeNull();
  });

  it("drops an answer or error that lands after unmount", async () => {
    let resolve!: (d: DiffResponse) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    const first = renderView();
    first.unmount();
    resolve(diff);

    let reject!: (e: Error) => void;
    fetchMock.mockReturnValueOnce(new Promise((_, r) => (reject = r)));
    const second = renderView();
    const stub = second.getByTestId("diff-stub");
    second.unmount();
    reject(new Error("late"));
    await Promise.resolve();
    expect(stub.dataset.loading).toBe("true");
    expect(screen.queryByText("late")).toBeNull();
  });

  it("goes back and forwards file context menus", async () => {
    fetchMock.mockResolvedValue(diff);
    const user = userEvent.setup();
    const { onBack, onFileContextMenu } = renderView();
    const back = screen.getByTestId("commit-diff-back");
    fireEvent.mouseEnter(back);
    expect(back.style.color).not.toBe("");
    const hovered = back.style.color;
    fireEvent.mouseLeave(back);
    expect(back.style.color).not.toBe(hovered);

    await user.click(back);
    expect(onBack).toHaveBeenCalledTimes(1);

    fireEvent.contextMenu(screen.getByText("menu a.go"));
    expect(onFileContextMenu).toHaveBeenCalledWith(expect.anything(), "a.go");
  });

  it("expands and collapses the commit body from the subject", async () => {
    fetchMock.mockResolvedValue(diff);
    const user = userEvent.setup();
    renderView();
    const subject = screen.getByTestId("commit-diff-subject");
    expect(subject.title).toBe("Expand full message");
    expect(subject.textContent).toContain("▸");

    await user.click(subject);
    expect(subject.title).toBe("Collapse message");
    expect(screen.getByText("Details here")).toBeTruthy();

    await user.click(subject);
    expect(screen.queryByText("Details here")).toBeNull();
  });

  it("makes the subject inert for a commit without a body", async () => {
    fetchMock.mockResolvedValue(diff);
    const user = userEvent.setup();
    renderView({ commit: { ...commit, body: "  " } });
    const subject = screen.getByTestId("commit-diff-subject");
    expect(subject.title).toBe("");
    expect(subject.style.cursor).toBe("default");
    await user.click(subject);
    expect(subject.textContent).toBe("Add login");
  });
});
