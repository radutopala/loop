import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewComment } from "../../api/review";
import { PushSpinner, REVIEW_PUSH_KEYFRAMES, ReviewDiffView, ReviewPushContext } from "./ReviewDiffView";

// a.go has two hunks with a gap between them, a deleted line and an empty
// context line; b.go carries one added line; c.go is never commented.
const DIFF = `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package main
-func old() {}
+func one() {}
+func two() {}
@@ -20,2 +21,3 @@

+var y = 2
diff --git a/b.go b/b.go
index 333..444 100644
--- a/b.go
+++ b/b.go
@@ -1,2 +1,3 @@
 package b
+var z = 3
diff --git a/c.go b/c.go
index 555..666 100644
--- a/c.go
+++ b/c.go
@@ -1,1 +1,2 @@
 package c
+var w = 4
`;

function comment(id: string, path: string, line: number, extra: Partial<ReviewComment> = {}): ReviewComment {
  return { id, path, line, side: "RIGHT", body: `body ${id}`, pushed: false, ...extra };
}

type Rect = { top: number; bottom: number };

// jsdom has no layout. Elements listed in rects by data-testid get that box;
// every other element (the scroll container among them) spans 0..500.
function stubLayout(rects: Map<string, Rect>) {
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
    const r = rects.get(this.getAttribute("data-testid") ?? "") ?? { top: 0, bottom: 500 };
    return { top: r.top, bottom: r.bottom, height: r.bottom - r.top, left: 0, right: 0, width: 0, x: 0, y: r.top, toJSON: () => ({}) } as DOMRect;
  });
}

// Queue animation frames so a test runs them when it chooses.
function stubFrames() {
  const frames = new Map<number, FrameRequestCallback>();
  let next = 0;
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
    next++;
    frames.set(next, cb);
    return next;
  });
  const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation((id) => {
    frames.delete(id);
  });
  const flush = () =>
    act(() => {
      for (const [id, cb] of [...frames]) {
        frames.delete(id);
        cb(0);
      }
    });
  return { flush, cancel, pending: () => frames.size };
}

function setup(over: Partial<React.ComponentProps<typeof ReviewDiffView>> = {}) {
  const props = {
    channelId: "ch1",
    rawDiff: DIFF,
    comments: [] as ReviewComment[],
    onPushComment: vi.fn(),
    onDiscussComment: vi.fn(),
    onWhyComment: vi.fn(),
    onAddressComment: vi.fn(),
    onDeleteComment: vi.fn(),
    ...over,
  };
  const utils = render(<ReviewDiffView {...props} />);
  return { ...utils, props, rerender: (more: Partial<typeof props>) => utils.rerender(<ReviewDiffView {...props} {...more} />) };
}

const navCounter = () => within(screen.getByTestId("review-comment-nav")).getByTitle(/^Comment /);

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("ReviewDiffView empty states", () => {
  it("says there is nothing to show with no diff and no comments", () => {
    setup({ rawDiff: "" });
    expect(screen.getByText(/No diff content/)).toBeTruthy();
    expect(screen.queryByTestId("review-diff-toolbar")).toBeNull();
  });

  it("shows only the out-of-diff comments when the diff is empty", () => {
    setup({ rawDiff: "", comments: [comment("o1", "gone.go", 4)] });
    expect(screen.queryByTestId("review-diff-toolbar")).toBeNull();
    const orphans = screen.getByTestId("review-diff-orphans");
    expect(within(orphans).getByText("Comments outside the current diff (1)")).toBeTruthy();
    expect(within(orphans).getByText("gone.go:4")).toBeTruthy();
    expect(screen.getByTestId("review-comment-o1")).toBeTruthy();
  });

  it("reports no commented files when nothing is commented", () => {
    setup();
    expect(screen.getByText("0 files commented")).toBeTruthy();
    expect(screen.queryByTestId("review-comment-nav")).toBeNull();
    // With no comments every file starts collapsed and the first is focused.
    expect(screen.queryByText("package main")).toBeNull();
    expect(screen.getByTestId("review-diff-toolbar").textContent).toContain("a.go");
  });
});

describe("ReviewDiffView files", () => {
  it("expands the commented files, counts their comments and sums their lines", () => {
    setup({ comments: [comment("c1", "b.go", 2), comment("g1", "b.go", 1, { source: "github" })] });
    expect(screen.getByTestId("review-diff-file-count-b.go").textContent).toBe("2");
    expect(screen.getByTestId("review-diff-file-count-b.go").getAttribute("title")).toBe("1 agent · 1 github");
    expect(screen.queryByTestId("review-diff-file-count-a.go")).toBeNull();
    const a = screen.getByTestId("review-diff-file-a.go");
    expect(a.textContent).toContain("+3");
    expect(a.textContent).toContain("-1");
    expect(within(screen.getByTestId("review-diff-file-b.go")).getByText("var z = 3")).toBeTruthy();
    expect(within(a).queryByText("package main")).toBeNull();
    // The focus starts on the first commented file.
    expect(screen.getByText("1 / 1 files commented")).toBeTruthy();
    expect(screen.getByTestId("review-diff-toolbar").textContent).toContain("b.go");
  });

  it("toggles a file open and closed, focusing it", () => {
    setup({ comments: [comment("c1", "b.go", 2)] });
    const header = within(screen.getByTestId("review-diff-file-a.go")).getAllByRole("button")[0]!;
    fireEvent.click(header);
    const a = screen.getByTestId("review-diff-file-a.go");
    expect(within(a).getByText("package main")).toBeTruthy();
    expect(within(a).getByText("@@ -20,2 +21,3 @@")).toBeTruthy();
    // a.go is uncommented, so the commented-file counter has no position.
    expect(screen.getByText("– / 1 files commented")).toBeTruthy();
    fireEvent.click(header);
    expect(within(a).queryByText("package main")).toBeNull();
  });

  it("renders added, deleted and context lines with their markers and numbers", () => {
    setup({ comments: [comment("c1", "a.go", 2)] });
    const a = screen.getByTestId("review-diff-file-a.go");
    const del = within(a).getByText("func old() {}").parentElement!;
    expect(del.textContent).toBe("2−func old() {}");
    const add = within(a).getByText("func one() {}").parentElement!;
    expect(add.textContent).toBe("2+func one() {}");
    // The empty context line renders a space so the row keeps its height.
    const blank = a.querySelector('[data-review-line="0:1:0"]')!;
    expect(blank.textContent).toBe(" ");
    expect(blank.parentElement!.textContent).toBe("2021  ");
  });

  it("expands and collapses every file", () => {
    setup({ comments: [comment("c1", "b.go", 2)] });
    fireEvent.click(screen.getByTestId("review-diff-expand-all"));
    expect(screen.getByText("package main")).toBeTruthy();
    expect(screen.getByText("package c")).toBeTruthy();
    fireEvent.click(screen.getByTestId("review-diff-collapse-all"));
    expect(screen.queryByText("package main")).toBeNull();
    expect(screen.queryByText("package b")).toBeNull();
  });

  it("re-expands commented files when the comments change, and keeps the state when they don't", () => {
    const { rerender } = setup({ comments: [comment("c1", "b.go", 2)] });
    fireEvent.click(screen.getByTestId("review-diff-collapse-all"));
    rerender({ comments: [comment("c1", "b.go", 2)] });
    expect(screen.queryByText("package b")).toBeNull();
    rerender({ comments: [comment("c1", "b.go", 2), comment("c2", "c.go", 2)] });
    expect(screen.getByText("package b")).toBeTruthy();
    expect(screen.getByText("package c")).toBeTruthy();
  });

  it("steps between commented files with the toolbar", () => {
    const frames = stubFrames();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    setup({ comments: [comment("c1", "a.go", 2), comment("c2", "c.go", 2)] });
    const prev = screen.getByTestId("review-diff-prev-file") as HTMLButtonElement;
    const next = screen.getByTestId("review-diff-next-file") as HTMLButtonElement;
    expect(screen.getByText("1 / 2 files commented")).toBeTruthy();
    expect(prev.disabled).toBe(true);
    fireEvent.click(screen.getByTestId("review-diff-collapse-all"));
    fireEvent.click(next);
    expect(screen.getByText("2 / 2 files commented")).toBeTruthy();
    expect(next.disabled).toBe(true);
    // Stepping onto a collapsed file opens it.
    expect(screen.getByText("package c")).toBeTruthy();
    frames.flush();
    expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
    fireEvent.click(prev);
    expect(screen.getByText("1 / 2 files commented")).toBeTruthy();
    // a.go was collapsed too, and is open again.
    expect(screen.getByText("package main")).toBeTruthy();
    // Stepping onto a file already open leaves it open.
    fireEvent.click(next);
    fireEvent.click(prev);
    expect(screen.getByText("package main")).toBeTruthy();
  });

  it("counts out-of-diff paths among the commented files", () => {
    setup({ comments: [comment("c1", "b.go", 2), comment("o1", "gone.go", 1), comment("o2", "gone.go", 2), comment("o3", "a.go", 99)] });
    // b.go plus the two out-of-diff paths, gone.go and a.go.
    expect(screen.getByText("1 / 3 files commented")).toBeTruthy();
    expect(screen.getByText("Comments outside the current diff (3)")).toBeTruthy();
    expect(screen.getByText("a.go:99")).toBeTruthy();
  });

  it("anchors a LEFT-side comment under the deleted line", () => {
    setup({ comments: [comment("l1", "a.go", 2, { side: "LEFT" }), comment("r1", "a.go", 2)] });
    const a = screen.getByTestId("review-diff-file-a.go");
    // Old line 2 is the deleted line; new line 2 is the first added one.
    const del = within(a).getByText("func old() {}").closest("[data-review-line]")!.parentElement!.parentElement!;
    expect(within(del).getByTestId("review-comment-l1")).toBeTruthy();
    const add = within(a).getByText("func one() {}").closest("[data-review-line]")!.parentElement!.parentElement!;
    expect(within(add).getByTestId("review-comment-r1")).toBeTruthy();
  });
});

describe("ReviewDiffView file context menu", () => {
  it("opens the file in the editor", () => {
    setup({ comments: [comment("c1", "b.go", 2)] });
    const opened = vi.fn();
    window.addEventListener("loop:open-file", opened);
    fireEvent.contextMenu(within(screen.getByTestId("review-diff-file-b.go")).getAllByRole("button")[0]!, { clientX: 10, clientY: 20 });
    fireEvent.click(screen.getByText("Open file"));
    window.removeEventListener("loop:open-file", opened);
    expect((opened.mock.calls[0]![0] as CustomEvent).detail).toEqual({ channelId: "ch1", target: { rootIndex: 0, relPath: "b.go" }, line: null });
    expect(screen.queryByText("Open file")).toBeNull();
  });

  it("copies the relative and the absolute path", () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    setup({ comments: [comment("o1", "gone.go", 1)], worktreePath: "/home/u/wt", homeDir: "/home/u" });
    fireEvent.contextMenu(screen.getByText("gone.go:1"));
    fireEvent.click(screen.getByText("Copy relative path"));
    expect(writeText).toHaveBeenLastCalledWith("gone.go");
    fireEvent.contextMenu(screen.getByText("gone.go:1"));
    fireEvent.click(screen.getByText("Copy absolute path"));
    expect(writeText).toHaveBeenLastCalledWith("~/wt/gone.go");
  });

  it("copies a root-relative absolute path without a worktree", () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    setup();
    fireEvent.contextMenu(within(screen.getByTestId("review-diff-file-c.go")).getAllByRole("button")[0]!);
    fireEvent.click(screen.getByText("Copy absolute path"));
    expect(writeText).toHaveBeenCalledWith("/c.go");
  });
});

describe("ReviewDiffView comment cards", () => {
  it("labels an agent finding with its category, verdict and impact", () => {
    setup({
      comments: [
        comment("c1", "b.go", 2, { category: "correctness", verdict: "real", verdict_reason: "nil deref", impact: "blocking" }),
        comment("c2", "b.go", 2, { verdict: "false_positive", impact: "nice_to_have" }),
      ],
    });
    const c1 = screen.getByTestId("review-comment-c1");
    expect(within(c1).getByText("agent")).toBeTruthy();
    expect(screen.getByTestId("review-comment-category-c1").textContent).toBe("correctness");
    expect(screen.getByTestId("review-comment-verdict-c1").textContent).toBe("verified");
    expect(screen.getByTestId("review-comment-verdict-c1").getAttribute("title")).toBe("Dedup check: nil deref");
    expect(screen.getByTestId("review-comment-impact-c1").getAttribute("title")).toBe("Blocks approving the PR");
    expect(screen.getByTestId("review-comment-verdict-c2").getAttribute("title")).toBe("Dedup check against the code");
    expect(screen.getByTestId("review-comment-impact-c2").getAttribute("title")).toBe("Worth doing, but doesn't block approving the PR");
    expect(within(c1).getByText("body c1")).toBeTruthy();
    expect(within(c1).getByTitle("Discard this comment")).toBeTruthy();
  });

  it("labels GitHub threads and replies", () => {
    setup({
      comments: [
        comment("g1", "b.go", 2, {
          source: "github",
          author: "octo",
          github_id: 7,
          url: "https://example.test/c/7",
          outdated: true,
          resolved: true,
          pushed: true,
          thread: { status: "open", impact: "blocking", reason: "still racy" },
        }),
        comment("g2", "b.go", 2, { source: "github", github_id: 8, reply_to: 7, pushed: true }),
        comment("g3", "b.go", 1, { source: "github", github_id: 9, thread: { status: "addressed" } }),
      ],
    });
    const g1 = screen.getByTestId("review-comment-g1");
    expect(within(g1).getByText("@octo")).toBeTruthy();
    expect(screen.getByTestId("review-comment-thread-g1").textContent).toBe("still open");
    expect(screen.getByTestId("review-comment-thread-g1").getAttribute("title")).toBe("Verification: still racy");
    expect(screen.getByTestId("review-comment-impact-g1")).toBeTruthy();
    expect(within(g1).getByText("outdated")).toBeTruthy();
    expect(screen.getByTestId("review-comment-resolved-g1")).toBeTruthy();
    expect(g1.style.opacity).toBe("0.55");
    expect(within(g1).getByText("view").getAttribute("href")).toBe("https://example.test/c/7");
    expect(within(g1).getByText("on github")).toBeTruthy();
    expect(within(g1).getByTitle("Delete this comment from GitHub")).toBeTruthy();
    const g2 = screen.getByTestId("review-comment-g2");
    expect(within(g2).getByText("↳ github")).toBeTruthy();
    expect(g2.style.margin).toBe("2px 8px 2px 112px");
    expect(screen.getByTestId("review-comment-thread-g3").textContent).toBe("addressed");
    expect(screen.getByTestId("review-comment-thread-g3").getAttribute("title")).toBe("The verification's check of this thread");
    expect(screen.queryByTestId("review-comment-impact-g3")).toBeNull();
  });

  it("wires each action to its callback", async () => {
    const c = comment("c1", "b.go", 2);
    const { props } = setup({ comments: [c] });
    fireEvent.click(screen.getByTestId("review-comment-discuss-c1"));
    expect(props.onDiscussComment).toHaveBeenCalledWith(c);
    fireEvent.click(screen.getByTestId("review-comment-why-c1"));
    expect(props.onWhyComment).toHaveBeenCalledWith(c);
    fireEvent.click(screen.getByTestId("review-comment-push-c1"));
    expect(props.onPushComment).toHaveBeenCalledWith(c);
    fireEvent.click(screen.getByTestId("review-comment-delete-c1"));
    expect(props.onDeleteComment).toHaveBeenCalledWith(c);
    await act(async () => {
      fireEvent.click(screen.getByTestId("review-comment-address-c1"));
    });
    expect(props.onAddressComment).toHaveBeenCalledWith(c);
    expect(screen.getByTestId("review-comment-address-c1").textContent).toBe("Address");
  });

  it("disables Address while the fix request is sending, even when it fails", async () => {
    let fail!: (e: Error) => void;
    const onAddressComment = vi.fn(
      () =>
        new Promise<void>((_, reject) => {
          fail = reject;
        }),
    );
    setup({ comments: [comment("c1", "b.go", 2)], onAddressComment });
    const btn = screen.getByTestId("review-comment-address-c1") as HTMLButtonElement;
    fireEvent.click(btn);
    expect(btn.textContent).toBe("Sending...");
    expect(btn.disabled).toBe(true);
    fireEvent.click(btn);
    expect(onAddressComment).toHaveBeenCalledTimes(1);
    const unhandled = vi.fn();
    process.on("unhandledRejection", unhandled);
    await act(async () => {
      fail(new Error("offline"));
      await Promise.resolve();
    });
    process.off("unhandledRejection", unhandled);
    expect(btn.textContent).toBe("Address");
    expect(btn.disabled).toBe(false);
  });

  it("shows the push state the panel provides", () => {
    render(
      <ReviewPushContext.Provider value={{ pushing: new Set(["p1"]), landed: new Set(["l1", "l2"]) }}>
        <ReviewDiffView
          channelId="ch1"
          rawDiff={DIFF}
          comments={[comment("p1", "b.go", 2), comment("l1", "b.go", 2, { pushed: true }), comment("l2", "b.go", 2), comment("d1", "b.go", 2, { pushed: true })]}
          onPushComment={vi.fn()}
          onDiscussComment={vi.fn()}
          onWhyComment={vi.fn()}
          onAddressComment={vi.fn()}
          onDeleteComment={vi.fn()}
        />
      </ReviewPushContext.Provider>,
    );
    const p1 = screen.getByTestId("review-comment-p1");
    expect(p1.getAttribute("data-push-state")).toBe("pushing");
    const push = screen.getByTestId("review-comment-push-p1") as HTMLButtonElement;
    expect(push.disabled).toBe(true);
    expect(push.textContent).toBe("Pushing…");
    expect(push.getAttribute("title")).toBe("Pushing this comment to GitHub");
    expect(screen.getByTestId("review-comment-l1").getAttribute("data-push-state")).toBe("landed");
    expect(screen.getByTestId("review-comment-pushed-l1").textContent).toBe("✓ Pushed to GitHub");
    // Landed but not yet marked pushed: the button is back.
    expect(screen.getByTestId("review-comment-push-l2").textContent).toBe("Push to GitHub");
    const d1 = screen.getByTestId("review-comment-d1");
    expect(d1.getAttribute("data-push-state")).toBeNull();
    expect(within(d1).getByText("pushed")).toBeTruthy();
  });

  it("outlines the latest review's findings", () => {
    setup({ comments: [comment("n1", "b.go", 2, { run_id: 5 }), comment("o1", "b.go", 2, { run_id: 4 })], reviewRuns: [5] });
    expect(screen.getByTestId("review-comment-n1").style.outline).not.toBe("");
    expect(screen.getByTestId("review-comment-o1").style.outline).toBe("");
  });
});

describe("PushSpinner", () => {
  it("draws at the given size, 8px by default", () => {
    const { container, rerender } = render(<PushSpinner color="red" />);
    expect((container.firstChild as HTMLElement).style.width).toBe("8px");
    rerender(<PushSpinner color="red" size={12} />);
    expect((container.firstChild as HTMLElement).style.width).toBe("12px");
    expect(REVIEW_PUSH_KEYFRAMES).toContain("loop-review-push-spin");
  });
});

describe("ReviewDiffView comment navigator", () => {
  const three = () => [comment("a1", "a.go", 2, { run_id: 1, impact: "blocking" }), comment("b1", "b.go", 2, { run_id: 1 }), comment("o1", "gone.go", 3, { run_id: 1 })];

  it("walks the comments in order, landing on each and flashing it", () => {
    const frames = stubFrames();
    const rects = new Map<string, Rect>([
      ["review-comment-a1", { top: 100, bottom: 140 }],
      ["review-comment-b1", { top: 300, bottom: 340 }],
      ["review-comment-o1", { top: 900, bottom: 940 }],
    ]);
    stubLayout(rects);
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const animate = vi.fn();
    (Element.prototype as unknown as { animate: unknown }).animate = animate;
    setup({ comments: three(), reviewRuns: [1] });
    // Nothing landed: the counter reports the comment nearest the middle.
    expect(navCounter().textContent).toBe("2 / 3");
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    // The first comment below the top of the view is a1.
    expect(navCounter().textContent).toBe("1 / 3");
    frames.flush();
    expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "center" });
    expect(animate).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("review-comment-a1").style.background).not.toBe(screen.getByTestId("review-comment-b1").style.background);
    expect((screen.getByTestId("review-comment-nav-prev") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("3 / 3");
    expect((screen.getByTestId("review-comment-nav-next") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("review-comment-nav-prev"));
    expect(navCounter().textContent).toBe("2 / 3");
    delete (Element.prototype as unknown as { animate?: unknown }).animate;
  });

  it("steps back to the last comment above the bottom of the view before landing", () => {
    stubFrames();
    stubLayout(
      new Map([
        ["review-comment-a1", { top: 100, bottom: 140 }],
        ["review-comment-b1", { top: 300, bottom: 340 }],
        ["review-comment-o1", { top: 900, bottom: 940 }],
      ]),
    );
    setup({ comments: three(), reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-comment-nav-prev"));
    expect(navCounter().textContent).toBe("2 / 3");
  });

  it("falls back to the counter's comment when none is on screen", () => {
    stubFrames();
    stubLayout(new Map([["review-comment-a1", { top: -100, bottom: -50 }]]));
    setup({ comments: [comment("a1", "a.go", 2, { run_id: 1 })], reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-comment-nav-prev"));
    expect(navCounter().textContent).toBe("1 / 1");
  });

  it("opens a collapsed file to reach its comment, and skips the scroll when the card is gone", () => {
    const frames = stubFrames();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    setup({ comments: [comment("a1", "a.go", 2, { run_id: 1 }), comment("b1", "b.go", 2, { run_id: 1 })], reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-diff-collapse-all"));
    // Collapsed out of the DOM, the counter keeps its index.
    fireEvent.scroll(screen.getByTestId("review-diff-file-a.go").parentElement!.parentElement!);
    frames.flush();
    expect(navCounter().textContent).toBe("1 / 2");
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(screen.getByTestId("review-comment-a1")).toBeTruthy();
    // The landed card is unmounted before its frame runs.
    fireEvent.click(screen.getByTestId("review-diff-collapse-all"));
    frames.flush();
    expect(scroll).not.toHaveBeenCalled();
  });

  it("lands on an out-of-diff comment without touching the files", () => {
    stubFrames();
    stubLayout(new Map([["review-comment-o1", { top: 100, bottom: 140 }]]));
    setup({ comments: [comment("o1", "gone.go", 3, { run_id: 1 })], reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("1 / 1");
    // The out-of-diff path counts as commented, but no file row is focused on it.
    expect(screen.getByText("– / 1 files commented")).toBeTruthy();
  });

  it("keeps the landed comment while it is on screen and lets go once it scrolls away", () => {
    vi.useFakeTimers();
    const frames = stubFrames();
    const rects = new Map<string, Rect>([
      ["review-comment-a1", { top: 100, bottom: 140 }],
      ["review-comment-b1", { top: 240, bottom: 260 }],
      ["review-comment-o1", { top: 900, bottom: 940 }],
    ]);
    stubLayout(rects);
    setup({ comments: three(), reviewRuns: [1] });
    const sc = screen.getByTestId("review-diff-file-a.go").parentElement!.parentElement!;
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("1 / 3");
    // While the jump scrolls, scrolling doesn't move the counter.
    fireEvent.scroll(sc);
    frames.flush();
    expect(navCounter().textContent).toBe("1 / 3");
    // A scroll end releases the jump; another one without a jump is a no-op.
    fireEvent(sc, new Event("scrollend"));
    fireEvent(sc, new Event("scrollend"));
    // b1 now sits nearest the middle, but a1 is still on screen.
    fireEvent.scroll(sc);
    // A second scroll in the same frame waits for that frame.
    fireEvent.scroll(sc);
    expect(frames.pending()).toBe(1);
    frames.flush();
    expect(navCounter().textContent).toBe("1 / 3");
    expect((screen.getByTestId("review-comment-nav-prev") as HTMLButtonElement).disabled).toBe(true);
    rects.set("review-comment-a1", { top: -100, bottom: -60 });
    fireEvent.scroll(sc);
    frames.flush();
    expect(navCounter().textContent).toBe("2 / 3");
    // Not landed any more: both directions are open.
    expect((screen.getByTestId("review-comment-nav-prev") as HTMLButtonElement).disabled).toBe(false);
  });

  it("lets go of a landed comment the scope no longer walks", () => {
    vi.useFakeTimers();
    stubFrames();
    stubLayout(
      new Map([
        ["review-comment-a1", { top: 100, bottom: 140 }],
        ["review-comment-x1", { top: 240, bottom: 260 }],
      ]),
    );
    setup({ comments: [comment("a1", "a.go", 2), comment("x1", "b.go", 2, { run_id: 1 })], reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-comment-nav-scope-all"));
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("1 / 2");
    act(() => vi.advanceTimersByTime(1000));
    fireEvent.click(screen.getByTestId("review-comment-nav-scope-new"));
    expect(navCounter().textContent).toBe("1 / 1");
    expect((screen.getByTestId("review-comment-nav-prev") as HTMLButtonElement).disabled).toBe(false);
  });

  it("ignores a step past the end when comments arrived mid-jump", () => {
    vi.useFakeTimers();
    stubFrames();
    stubLayout(new Map());
    const { rerender } = setup({ comments: [comment("b1", "b.go", 2, { run_id: 1 })], reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("1 / 1");
    // A finding streams in above while the jump is in flight, so the counter
    // keeps its stale index and next stays enabled with b1 last.
    rerender({ comments: [comment("a1", "a.go", 2, { run_id: 1 }), comment("b1", "b.go", 2, { run_id: 1 })] });
    expect(navCounter().textContent).toBe("1 / 2");
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("1 / 2");
  });

  it("clamps the counter when a sync drops the comment it pointed at", () => {
    vi.useFakeTimers();
    stubFrames();
    stubLayout(new Map());
    const { rerender } = setup({ comments: [comment("a1", "a.go", 2, { run_id: 1 }), comment("b1", "b.go", 2, { run_id: 1 })], reviewRuns: [1] });
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    fireEvent.click(screen.getByTestId("review-comment-nav-next"));
    expect(navCounter().textContent).toBe("2 / 2");
    rerender({ comments: [comment("a1", "a.go", 2, { run_id: 1 })] });
    expect(navCounter().textContent).toBe("1 / 1");
  });

  it("switches scope and kind, remembering both", () => {
    setup({ comments: three().concat(comment("old", "b.go", 1, { run_id: 0 })), reviewRuns: [1] });
    const scopeNew = screen.getByTestId("review-comment-nav-scope-new");
    expect(scopeNew.getAttribute("aria-pressed")).toBe("true");
    expect(navCounter().textContent).toMatch(/\/ 3$/);
    fireEvent.click(screen.getByTestId("review-comment-nav-scope-all"));
    expect(window.localStorage.getItem("loop-review-comment-nav-scope")).toBe("all");
    expect(navCounter().textContent).toMatch(/\/ 4$/);
    fireEvent.change(screen.getByTestId("review-comment-nav-kind"), { target: { value: "blocking" } });
    expect(window.localStorage.getItem("loop-review-comment-nav-kind")).toBe("blocking");
    expect(navCounter().textContent).toBe("1 / 1");
    fireEvent.change(screen.getByTestId("review-comment-nav-kind"), { target: { value: "already_fixed" } });
    expect(navCounter().textContent).toBe("0 / 0");
    expect((screen.getByTestId("review-comment-nav-next") as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId("review-comment-nav-prev") as HTMLButtonElement).disabled).toBe(true);
  });

  it("starts from the remembered scope and kind, ignoring an unknown kind", () => {
    window.localStorage.setItem("loop-review-comment-nav-scope", "all");
    window.localStorage.setItem("loop-review-comment-nav-kind", "blocking");
    const { unmount } = setup({ comments: three() });
    expect(screen.getByTestId("review-comment-nav-scope-all").getAttribute("aria-pressed")).toBe("true");
    expect((screen.getByTestId("review-comment-nav-kind") as HTMLSelectElement).value).toBe("blocking");
    unmount();
    window.localStorage.setItem("loop-review-comment-nav-kind", "bogus");
    setup({ comments: three() });
    expect((screen.getByTestId("review-comment-nav-kind") as HTMLSelectElement).value).toBe("any");
  });

  it("cancels a pending measure on unmount", () => {
    const frames = stubFrames();
    const { unmount } = setup({ comments: three(), reviewRuns: [1] });
    fireEvent.scroll(screen.getByTestId("review-diff-file-a.go").parentElement!.parentElement!);
    expect(frames.pending()).toBe(1);
    unmount();
    expect(frames.cancel).toHaveBeenCalled();
    expect(frames.pending()).toBe(0);
  });
});

describe("ReviewDiffView find in diff", () => {
  const open = () => fireEvent.click(screen.getByTestId("review-diff-search-toggle"));
  const input = () => screen.getByTestId("review-diff-search-input") as HTMLInputElement;
  const type = (v: string) => fireEvent.change(input(), { target: { value: v } });

  it("opens on Cmd+F or Ctrl+F in the pane and selects the box", () => {
    const frames = stubFrames();
    setup();
    const root = screen.getByTestId("review-diff-toolbar").parentElement!;
    fireEvent.mouseDown(root);
    expect(document.activeElement).toBe(root);
    fireEvent.keyDown(root, { key: "g", metaKey: true });
    fireEvent.keyDown(root, { key: "f" });
    expect(screen.queryByTestId("review-diff-search")).toBeNull();
    fireEvent.keyDown(root, { key: "f", metaKey: true });
    expect(screen.getByTestId("review-diff-search")).toBeTruthy();
    const select = vi.spyOn(input(), "select");
    frames.flush();
    expect(select).toHaveBeenCalled();
    open();
    expect(screen.queryByTestId("review-diff-search")).toBeNull();
    fireEvent.keyDown(root, { key: "f", ctrlKey: true });
    expect(screen.getByTestId("review-diff-search")).toBeTruthy();
  });

  it("names the shortcut for the platform", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    const { unmount } = setup();
    expect(screen.getByTestId("review-diff-search-toggle").getAttribute("title")).toBe("Find in diff (⌘F)");
    unmount();
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    setup();
    expect(screen.getByTestId("review-diff-search-toggle").getAttribute("title")).toBe("Find in diff (Ctrl+F)");
  });

  it("counts matches, opens the file of the active one and scrolls to it", () => {
    const frames = stubFrames();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    setup();
    open();
    type("package");
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("1 / 3");
    // The first match's file opens, then the line scrolls into view.
    expect(screen.getByTestId("review-diff-file-a.go").querySelector('[data-review-line="0:0:0"]')!.textContent).toBe("package main");
    frames.flush();
    expect(scroll).toHaveBeenCalledWith({ block: "center", behavior: "smooth" });
    expect(scroll.mock.contexts.at(-1)).toBe(screen.getByTestId("review-diff-file-a.go").querySelector('[data-review-line="0:0:0"]'));
    // The match is highlighted.
    expect(within(screen.getByTestId("review-diff-file-a.go")).getByText("package").tagName).toBe("SPAN");
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("2 / 3");
    expect(screen.getByTestId("review-diff-toolbar").textContent).toContain("b.go");
    fireEvent.keyDown(input(), { key: "Enter", shiftKey: true });
    fireEvent.keyDown(input(), { key: "Enter", shiftKey: true });
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("3 / 3");
    fireEvent.click(screen.getByTitle("Next match (Enter)"));
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("1 / 3");
    fireEvent.click(screen.getByTitle("Previous match (Shift+Enter)"));
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("3 / 3");
    // Another key does nothing; a new term restarts at the first match.
    fireEvent.keyDown(input(), { key: "a" });
    type("var");
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("1 / 3");
  });

  it("cancels the scroll frame when the match moves on first", () => {
    const frames = stubFrames();
    setup({ comments: [comment("c1", "a.go", 2)] });
    open();
    type("func");
    expect(frames.pending()).toBe(2);
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(frames.cancel).toHaveBeenCalled();
  });

  it("reports no matches and disables stepping", () => {
    setup();
    open();
    type("nothing-here");
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("no matches");
    expect((screen.getByTitle("Next match (Enter)") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(screen.getByTestId("review-diff-search-count").textContent).toBe("no matches");
    // A blank term shows no count.
    type("  ");
    expect(screen.queryByTestId("review-diff-search-count")).toBeNull();
  });

  it("closes on Escape or the close button, clearing the query", () => {
    setup();
    open();
    type("package");
    fireEvent.keyDown(input(), { key: "Escape" });
    expect(screen.queryByTestId("review-diff-search")).toBeNull();
    open();
    expect(input().value).toBe("");
    fireEvent.click(screen.getByTitle("Close (Esc)"));
    expect(screen.queryByTestId("review-diff-search")).toBeNull();
  });

  it("jumps to a file with > and the arrow keys", () => {
    stubFrames();
    setup();
    open();
    type(">");
    expect(screen.getByText("jump to file")).toBeTruthy();
    // No results: the arrows and Enter do nothing.
    fireEvent.keyDown(input(), { key: "ArrowDown" });
    fireEvent.keyDown(input(), { key: "Enter" });
    fireEvent.keyDown(input(), { key: "x" });
    expect(screen.getByTestId("review-diff-search")).toBeTruthy();
    type(">.go");
    expect(screen.getByText("3 files")).toBeTruthy();
    const rows = () => screen.getAllByTestId("review-diff-search-path-result");
    expect(rows().map((r) => r.textContent)).toEqual(["a.go", "b.go", "c.go"]);
    fireEvent.keyDown(input(), { key: "ArrowUp" });
    fireEvent.keyDown(input(), { key: "ArrowUp" });
    expect(rows()[1]!.style.background).not.toBe(rows()[0]!.style.background);
    fireEvent.keyDown(input(), { key: "ArrowDown" });
    fireEvent.keyDown(input(), { key: "Enter" });
    expect(screen.queryByTestId("review-diff-search")).toBeNull();
    expect(screen.getByTestId("review-diff-toolbar").textContent).toContain("c.go");
    expect(screen.getByText("package c")).toBeTruthy();
  });

  it("jumps to a file picked from the list", () => {
    stubFrames();
    setup();
    open();
    type(">b.go");
    expect(screen.getByText("1 file")).toBeTruthy();
    fireEvent.click(screen.getByTestId("review-diff-search-path-result"));
    expect(screen.queryByTestId("review-diff-search")).toBeNull();
    expect(screen.getByText("package b")).toBeTruthy();
  });
});
