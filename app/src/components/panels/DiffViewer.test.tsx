import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type DiffFile, fetchFileContent } from "../../api/loopApi";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors } from "../../theme";
import { DiffViewer, fileKey, highlightContent, type ParsedFile, parseUnifiedDiff } from "./DiffViewer";

vi.mock("../../api/loopApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/loopApi")>()),
  fetchFileContent: vi.fn(),
}));

// A 200-line file changed at lines 51 and 101: a 49-line gap above the first
// hunk, a 47-line gap between the hunks and, once the file is read, a 99-line
// gap below the last.
const BIG_DIFF = `diff --git a/big.go b/big.go
--- a/big.go
+++ b/big.go
@@ -50,3 +50,3 @@
 line 50
-old 51
+new 51
 line 52
@@ -100,2 +100,2 @@
 line 100
-old 101
+new 101
`;
const BIG_CONTENT = Array.from({ length: 200 }, (_, i) => `line ${i + 1}`).join("\n");

// A 10-line file changed at line 5: a 4-line gap above it.
const SMALL_DIFF = `diff --git a/small.go b/small.go
--- a/small.go
+++ b/small.go
@@ -5,2 +5,2 @@
 five
-six
+SIX
`;

const NEW_DIFF = `diff --git a/fresh.txt b/fresh.txt
--- /dev/null
+++ b/fresh.txt
@@ -0,0 +1,2 @@
+hello needle
+
`;

const bigFile: DiffFile = { path: "big.go", additions: 2, deletions: 2, binary: false, status: "unstaged" };
const smallFile: DiffFile = { path: "small.go", additions: 1, deletions: 1, binary: false, status: "staged" };
const newFile: DiffFile = { path: "fresh.txt", additions: 2, deletions: 0, binary: false, status: "untracked" };
const imageFile: DiffFile = { path: "img.png", additions: 0, deletions: 0, binary: true, status: "conflict" };

const allFiles = [bigFile, smallFile, newFile, imageFile];
const allParsed: ParsedFile[] = [
  ...parseUnifiedDiff(BIG_DIFF, "unstaged"),
  ...parseUnifiedDiff(SMALL_DIFF, "staged"),
  ...parseUnifiedDiff(NEW_DIFF, "untracked"),
  { path: "img.png", hunks: [], status: "conflict" },
];

interface HarnessProps {
  files?: DiffFile[];
  parsedFiles?: ParsedFile[];
  expanded?: string[];
  channelId?: string | null;
  loading?: boolean;
  hasData?: boolean;
  totalFiles?: number;
  onToggle?: (key: string) => void;
  onFileContextMenu?: (e: React.MouseEvent, path: string) => void;
  rootIndex?: number;
  fileRef?: string;
}

/** Holds expandedFiles like the Git panel does, so toggling re-renders. */
function Harness({
  files = allFiles,
  parsedFiles = allParsed,
  expanded = [],
  channelId = "ch",
  loading = false,
  hasData = true,
  totalFiles,
  onToggle,
  onFileContextMenu = () => {},
  rootIndex,
  fileRef,
}: HarnessProps) {
  const [open, setOpen] = useState(new Set(expanded));
  return (
    <DiffViewer
      channelId={channelId}
      files={files}
      parsedFiles={parsedFiles}
      expandedFiles={open}
      loading={loading}
      hasData={hasData}
      totalFiles={totalFiles ?? files.length}
      onToggleFile={(key) => {
        onToggle?.(key);
        setOpen((prev) => {
          const next = new Set(prev);
          if (next.has(key)) next.delete(key);
          else next.add(key);
          return next;
        });
      }}
      onExpandAll={() => setOpen(new Set(files.map(fileKey)))}
      onCollapseAll={() => setOpen(new Set())}
      onFileContextMenu={onFileContextMenu}
      rootIndex={rootIndex}
      fileRef={fileRef}
    />
  );
}

const renderViewer = (props: HarnessProps = {}) =>
  render(
    <ThemeProvider initialTheme="dark">
      <Harness {...props} />
    </ThemeProvider>,
  );

const header = (path: string) => screen.getByText(path, { selector: "bdi" }).closest("button")!;
const fileRow = (index: number) => document.querySelector<HTMLElement>(`[data-file-idx="${index}"]`)!;
const navPath = () => screen.getByTitle("Find in diff (Ctrl+F)").nextElementSibling as HTMLElement;
const counter = () => navPath().nextElementSibling!.textContent;
const openSearch = () => {
  fireEvent.click(screen.getByTestId("diff-search-toggle"));
  return screen.getByTestId("diff-search-input") as HTMLInputElement;
};
const rgb = (color: string) => {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
};

beforeEach(() => {
  vi.mocked(fetchFileContent).mockReset();
  vi.mocked(fetchFileContent).mockResolvedValue({ content: BIG_CONTENT, binary: false });
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("DiffViewer file list", () => {
  it("shows loading before data and nothing else without files", () => {
    renderViewer({ files: [], parsedFiles: [], loading: true, hasData: false });
    expect(screen.getByText("Loading...")).toBeTruthy();
    expect(screen.queryByTitle("Expand all")).toBeNull();
  });

  it("says there are no changes once the diff is empty", () => {
    // An expanded key with no files left must not break the focused-path read.
    renderViewer({ files: [], parsedFiles: [], expanded: [":gone"], totalFiles: 0 });
    expect(screen.getByText("No changes")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("lists each file with its status badge, counts or binary marker", () => {
    renderViewer();
    const badge = (path: string) => within(header(path)).getAllByText(/^(unstaged|staged|new|conflict)$/)[0]!;
    expect(badge("big.go").textContent).toBe("unstaged");
    expect(badge("big.go").style.color).toBe(rgb(darkColors.warning));
    expect(badge("small.go").style.color).toBe(rgb(darkColors.active));
    expect(badge("fresh.txt").textContent).toBe("new");
    expect(badge("fresh.txt").style.color).toBe(rgb(darkColors.textDim));
    expect(badge("img.png").style.color).toBe(rgb(darkColors.dangerText));
    expect(within(header("big.go")).getByText("+2").textContent).toBe("+2");
    expect(within(header("img.png")).getByText("binary")).toBeTruthy();
    expect(counter()).toBe("1 / 4");
    expect(navPath().textContent).toBe("");
  });

  it("shows a rename as git does and a file without status without a badge", () => {
    const renamed: DiffFile = { path: "pkg/b/util.go", old_path: "pkg/a/util.go", additions: 0, deletions: 0, binary: false };
    renderViewer({ files: [renamed], parsedFiles: [] });
    const row = screen.getByText("pkg/{a => b}/util.go").closest("button")!;
    expect(within(row).queryByText(/staged|new|conflict/)).toBeNull();
  });

  it("toggles a file open, shows its hunks, and tracks focus", () => {
    const onToggle = vi.fn();
    renderViewer({ onToggle });
    fireEvent.click(header("small.go"));
    expect(onToggle).toHaveBeenCalledWith("staged:small.go");
    expect(screen.getByText("@@ -5,2 +5,2 @@")).toBeTruthy();
    expect(screen.getByText("SIX")).toBeTruthy();
    expect(navPath().textContent).toBe("small.go");
    expect(counter()).toBe("2 / 4");
    expect(fileRow(1).style.borderLeft).toContain(rgb(darkColors.active));
    fireEvent.click(header("small.go"));
    expect(screen.queryByText("SIX")).toBeNull();
  });

  it("highlights the header on hover only while the file is collapsed", () => {
    renderViewer({ expanded: ["staged:small.go"] });
    const collapsed = header("big.go");
    fireEvent.mouseEnter(collapsed);
    expect(collapsed.style.background).toBe(rgb(darkColors.hoverBg).replace("color", "background"));
    fireEvent.mouseLeave(collapsed);
    expect(collapsed.style.background).toBe("transparent");
    const open = header("small.go");
    const before = open.style.background;
    fireEvent.mouseEnter(open);
    fireEvent.mouseLeave(open);
    expect(open.style.background).toBe(before);
  });

  it("hands the file to the context menu", () => {
    const onFileContextMenu = vi.fn();
    renderViewer({ onFileContextMenu });
    fireEvent.contextMenu(header("big.go"));
    expect(onFileContextMenu).toHaveBeenCalledWith(expect.anything(), "big.go");
  });

  it("expands and collapses every file", () => {
    renderViewer();
    const expandAll = screen.getByTitle("Expand all");
    const collapseAll = screen.getByTitle("Collapse all");
    for (const b of [expandAll, collapseAll]) {
      fireEvent.mouseEnter(b);
      expect(b.style.color).toBe(rgb(darkColors.textLight));
      fireEvent.mouseLeave(b);
      expect(b.style.color).toBe(rgb(darkColors.textMuted));
    }
    fireEvent.click(expandAll);
    expect(screen.getByText("SIX")).toBeTruthy();
    expect(screen.getByText("Binary file — no content preview")).toBeTruthy();
    fireEvent.click(collapseAll);
    expect(screen.queryByText("SIX")).toBeNull();
  });

  it("renders a new file's lines with + markers and a blank line as a space", () => {
    renderViewer({ expanded: ["untracked:fresh.txt"] });
    expect(screen.getByText("hello needle")).toBeTruthy();
    expect(screen.getAllByText("+").length).toBeGreaterThanOrEqual(2);
  });

  it("shows nothing under an expanded file that has no parsed diff", () => {
    renderViewer({ files: [bigFile], parsedFiles: [], expanded: ["unstaged:big.go"] });
    expect(screen.queryByText(/^@@/)).toBeNull();
  });

  it("focuses the file under the mouse", () => {
    renderViewer({ expanded: ["unstaged:big.go"] });
    fireEvent.mouseEnter(fileRow(2));
    expect(navPath().textContent).toBe("fresh.txt");
  });

  it("drops a row that goes away and starts again at the first file", () => {
    const { rerender } = renderViewer({ expanded: ["unstaged:big.go"] });
    fireEvent.mouseEnter(fileRow(2));
    rerender(
      <ThemeProvider initialTheme="dark">
        <Harness files={[bigFile]} expanded={["unstaged:big.go"]} />
      </ThemeProvider>,
    );
    expect(document.querySelectorAll("[data-file-idx]")).toHaveLength(1);
    expect(navPath().textContent).toBe("big.go");
  });

  it("focuses the root on mouse down", () => {
    const { container } = renderViewer();
    const root = container.querySelector<HTMLElement>("[tabindex='-1']")!;
    fireEvent.mouseDown(root);
    expect(document.activeElement).toBe(root);
  });
});

describe("DiffViewer file navigation", () => {
  it("steps to the next and previous file, opening it and scrolling to it", async () => {
    vi.useFakeTimers();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const onToggle = vi.fn();
    renderViewer({ expanded: ["unstaged:big.go"], onToggle });
    const prev = screen.getByTitle("Previous file") as HTMLButtonElement;
    const next = screen.getByTitle("Next file") as HTMLButtonElement;
    expect(prev.disabled).toBe(true);
    fireEvent.click(next);
    expect(onToggle).toHaveBeenCalledWith("staged:small.go");
    expect(counter()).toBe("2 / 4");
    await act(() => vi.advanceTimersByTimeAsync(20));
    expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
    expect(scroll.mock.contexts.at(-1)).toBe(fileRow(1));

    // Hovering while the scroll runs doesn't steal the focus back.
    fireEvent.mouseEnter(fileRow(3));
    expect(counter()).toBe("2 / 4");
    await act(() => vi.advanceTimersByTimeAsync(500));
    fireEvent.mouseEnter(fileRow(3));
    expect(counter()).toBe("4 / 4");
    expect(next.disabled).toBe(true);

    onToggle.mockClear();
    fireEvent.click(prev);
    expect(counter()).toBe("3 / 4");
    fireEvent.click(prev);
    // small.go is already open, so stepping back to it opens nothing.
    expect(onToggle).toHaveBeenCalledTimes(1);
    expect(onToggle).toHaveBeenCalledWith("untracked:fresh.txt");
  });
});

describe("DiffViewer context expansion", () => {
  const gapButton = (label: RegExp, n = 0) => screen.getAllByRole("button", { name: label })[n]!;

  it("offers step buttons on large gaps and reveals lines in steps", async () => {
    renderViewer({ expanded: ["unstaged:big.go"], rootIndex: 1, fileRef: "abc" });
    // Top gap: no ↓ (nothing above it), middle gap: both directions.
    expect(screen.getAllByRole("button", { name: /^↓ 20 lines$/ })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: /^↑ 20 lines$/ })).toHaveLength(2);
    expect(gapButton(/^↕ all 49$/)).toBeTruthy();
    expect(gapButton(/^↕ all 47$/)).toBeTruthy();

    for (const b of [gapButton(/^↓ 20 lines$/), gapButton(/^↑ 20 lines$/), gapButton(/^↕ all 49$/)]) {
      fireEvent.mouseEnter(b);
      expect(b.style.textDecoration).toBe("underline");
      fireEvent.mouseLeave(b);
      expect(b.style.textDecoration).toBe("none");
    }

    // ↑ on the top gap reveals the 20 lines just above the first hunk.
    fireEvent.click(gapButton(/^↑ 20 lines$/, 0));
    await screen.findByText("line 49");
    expect(fetchFileContent).toHaveBeenCalledWith("ch", "big.go", 1, "abc");
    expect(screen.getByText("line 30")).toBeTruthy();
    expect(screen.queryByText("line 29")).toBeNull();
    expect(gapButton(/^Load all 29 lines$/)).toBeTruthy();

    // ↓ on the middle gap reveals the 20 lines just below the first hunk.
    fireEvent.click(gapButton(/^↓ 20 lines$/));
    await screen.findByText("line 72");
    expect(screen.queryByText("line 73")).toBeNull();
    expect(gapButton(/^Load all 27 lines$/)).toBeTruthy();

    // The file is cached: the bottom gap appears and nothing is read again.
    expect(gapButton(/^↕ all 99$/)).toBeTruthy();
    fireEvent.click(gapButton(/^↕ all 99$/));
    await screen.findByText("line 200");
    expect(fetchFileContent).toHaveBeenCalledTimes(1);

    fireEvent.click(gapButton(/^Load all 29 lines$/));
    await screen.findByText("line 1");
  });

  it("loads all of a small gap at once", async () => {
    vi.mocked(fetchFileContent).mockResolvedValue({ content: "one\ntwo\nthree\nfour\nfive\nSIX", binary: false });
    renderViewer({ files: [smallFile], expanded: ["staged:small.go"] });
    const all = gapButton(/^Load all 4 lines$/);
    fireEvent.mouseEnter(all);
    expect(all.style.textDecoration).toBe("underline");
    fireEvent.mouseLeave(all);
    expect(all.style.textDecoration).toBe("none");
    fireEvent.click(all);
    await screen.findByText("four");
    expect(fetchFileContent).toHaveBeenCalledWith("ch", "small.go", undefined, undefined);
    expect(screen.queryByRole("button", { name: /Load all/ })).toBeNull();
  });

  it.each([
    ["the file is binary", () => vi.mocked(fetchFileContent).mockResolvedValue({ content: "", binary: true })],
    ["reading the file fails", () => vi.mocked(fetchFileContent).mockRejectedValue(new Error("gone"))],
  ])("reveals no lines when %s", async (_name, arrange) => {
    arrange();
    renderViewer({ files: [smallFile], expanded: ["staged:small.go"] });
    fireEvent.click(gapButton(/^Load all 4 lines$/));
    await waitFor(() => expect(screen.queryByRole("button", { name: /Load all/ })).toBeNull());
    expect(screen.queryByText("four")).toBeNull();
  });

  it("reads nothing without a channel", async () => {
    renderViewer({ files: [smallFile], expanded: ["staged:small.go"], channelId: null });
    fireEvent.click(gapButton(/^Load all 4 lines$/));
    await waitFor(() => expect(screen.queryByRole("button", { name: /Load all/ })).toBeNull());
    expect(fetchFileContent).not.toHaveBeenCalled();
  });
});

describe("DiffViewer search", () => {
  it("opens with Cmd+F or Ctrl+F on the pane and ignores other keys", () => {
    const { container } = renderViewer();
    const root = container.querySelector<HTMLElement>("[tabindex='-1']")!;
    fireEvent.keyDown(root, { key: "g", metaKey: true });
    fireEvent.keyDown(root, { key: "f" });
    expect(screen.queryByTestId("diff-search-input")).toBeNull();
    fireEvent.keyDown(root, { key: "f", metaKey: true });
    expect(screen.getByTestId("diff-search-input")).toBeTruthy();
    fireEvent.keyDown(screen.getByTestId("diff-search-input"), { key: "Escape" });
    expect(screen.queryByTestId("diff-search-input")).toBeNull();
    expect(document.activeElement).toBe(root);
    fireEvent.keyDown(root, { key: "f", ctrlKey: true });
    expect(screen.getByTestId("diff-search-input")).toBeTruthy();
  });

  it("names the Mac shortcut on a Mac", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    renderViewer();
    expect(screen.getByTestId("diff-search-toggle").title).toBe("Find in diff (⌘F)");
  });

  it("selects the query when it opens and closes from the toggle or the close button", async () => {
    renderViewer();
    const input = openSearch();
    const select = vi.spyOn(input, "select");
    await waitFor(() => expect(select).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("diff-search-toggle"));
    expect(screen.queryByTestId("diff-search-input")).toBeNull();
    openSearch();
    fireEvent.click(screen.getByTitle("Close (Esc)"));
    expect(screen.queryByTestId("diff-search-input")).toBeNull();
  });

  it("counts content matches, opens the file of the active one and steps through them", async () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const onToggle = vi.fn();
    renderViewer({ onToggle });
    const input = openSearch();
    fireEvent.change(input, { target: { value: "zzz" } });
    expect(screen.getByTestId("diff-search-count").textContent).toBe("no matches");
    expect((screen.getByTitle("Next match (Enter)") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.getByTestId("diff-search-count").textContent).toBe("no matches");

    // "1" is once in each line of big.go's first hunk and "line 100", twice in each "101".
    fireEvent.change(input, { target: { value: "1" } });
    expect(onToggle).toHaveBeenCalledWith("unstaged:big.go");
    const count = () => screen.getByTestId("diff-search-count").textContent;
    expect(count()).toBe("1 / 7");
    await waitFor(() => expect(scroll).toHaveBeenCalledWith({ block: "center", behavior: "smooth" }));
    expect(scroll.mock.contexts.at(-1)).toBe(document.querySelector('[data-diff-line="0:0:1"]'));

    const active = () => Array.from(document.querySelectorAll<HTMLElement>("span")).filter((s) => s.style.backgroundColor === "rgb(245, 158, 11)");
    expect(active().map((s) => s.parentElement?.dataset.diffLine)).toEqual(["0:0:1"]);

    fireEvent.keyDown(input, { key: "Enter" });
    expect(count()).toBe("2 / 7");
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(count()).toBe("7 / 7");
    fireEvent.click(screen.getByTitle("Next match (Enter)"));
    expect(count()).toBe("1 / 7");
    fireEvent.click(screen.getByTitle("Previous match (Shift+Enter)"));
    expect(count()).toBe("7 / 7");
    fireEvent.keyDown(input, { key: "a" });
    expect(count()).toBe("7 / 7");
  });

  it("highlights matches in hunks but not in revealed context", async () => {
    vi.mocked(fetchFileContent).mockResolvedValue({ content: "one\ntwo\nthree\nfour\nfive\nSIX", binary: false });
    renderViewer({ files: [smallFile], expanded: ["staged:small.go"] });
    fireEvent.click(screen.getByRole("button", { name: /^Load all 4 lines$/ }));
    await screen.findByText("four");
    const input = openSearch();
    fireEvent.change(input, { target: { value: "f" } });
    // "four" (context) and "five" (hunk): only the hunk line counts.
    expect(screen.getByTestId("diff-search-count").textContent).toBe("1 / 1");
    expect(screen.getByText("four").querySelector("span")).toBeNull();
  });

  it("jumps to a file by path with the keyboard", () => {
    const onToggle = vi.fn();
    renderViewer({ onToggle });
    const input = openSearch();
    fireEvent.change(input, { target: { value: ">" } });
    expect(screen.getByText("jump to file")).toBeTruthy();
    expect(screen.queryByTitle("Next match (Enter)")).toBeNull();
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.getByTestId("diff-search-input")).toBeTruthy();

    fireEvent.change(input, { target: { value: "> fresh" } });
    expect(screen.getByText("1 file")).toBeTruthy();
    fireEvent.change(input, { target: { value: ">g" } });
    expect(screen.getByText("3 files")).toBeTruthy();
    const results = () => screen.getAllByTestId("diff-search-path-result");
    // "g" starts a word in small.go's "go" but sits mid-word in "big".
    expect(results().map((r) => r.textContent)).toEqual(["small.go", "big.go", "img.png"]);
    const selected = rgb(darkColors.selectedBg).replace("color", "background");
    expect(results()[0]!.style.background).toBe(selected);
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(results()[2]!.style.background).toBe(selected);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(results()[1]!.style.background).toBe(selected);
    fireEvent.keyDown(input, { key: "x" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onToggle).toHaveBeenCalledWith("unstaged:big.go");
    expect(screen.queryByTestId("diff-search-input")).toBeNull();
    expect(counter()).toBe("1 / 4");
  });

  it("jumps to a file by path with the mouse", () => {
    renderViewer();
    const input = openSearch();
    fireEvent.change(input, { target: { value: ">img" } });
    const [hit] = screen.getAllByTestId("diff-search-path-result");
    fireEvent.mouseEnter(hit!);
    fireEvent.click(hit!);
    expect(screen.queryByTestId("diff-search-input")).toBeNull();
    expect(counter()).toBe("4 / 4");
  });
});

describe("highlightContent", () => {
  it("wraps each match, the active one more strongly, keeping the text around them", () => {
    const { container } = render(
      <div>
        {highlightContent(
          "a needle b needle",
          [
            { start: 2, end: 8 },
            { start: 11, end: 17 },
          ],
          11,
        )}
      </div>,
    );
    const spans = Array.from(container.querySelectorAll("span"));
    expect(container.textContent).toBe("a needle b needle");
    expect(spans.map((s) => [s.textContent, s.style.backgroundColor])).toEqual([
      ["needle", "rgba(245, 158, 11, 0.3)"],
      ["needle", "rgb(245, 158, 11)"],
    ]);
  });

  it("handles a match that covers the whole text", () => {
    const { container } = render(<div>{highlightContent("all", [{ start: 0, end: 3 }], -1)}</div>);
    expect(container.querySelectorAll("span")).toHaveLength(1);
    expect(container.firstElementChild!.childNodes).toHaveLength(1);
  });
});
