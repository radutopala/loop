import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ExplainView } from "../../hooks/useExplain";
import { layoutExplainView, layoutExplanation } from "../../test/layout-fixtures";
import { ExplainPane } from "./ExplainPane";

const explanation = layoutExplanation;
const view = layoutExplainView;

function renderPane(explain: ExplainView, onClose = vi.fn()) {
  const r = render(<ExplainPane channelId="ch-1" explain={explain} onClose={onClose} />);
  return { ...r, onClose };
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("ExplainPane", () => {
  it("says there's nothing yet without explanations", () => {
    renderPane(view());
    expect(screen.getByTestId("explain-empty").textContent).toContain("No explanations yet");
    expect(screen.getByText("write-ups of the chat's turns")).toBeTruthy();
  });

  it("closes from its button, which lights up under the mouse", () => {
    const { onClose } = renderPane(view());
    const btn = screen.getByRole("button", { name: "Close the Explain view" });
    fireEvent.mouseEnter(btn);
    expect(btn.style.backgroundColor).toBe("rgb(54, 54, 54)");
    fireEvent.mouseLeave(btn);
    expect(btn.style.backgroundColor).toBe("transparent");
    fireEvent.click(btn);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("shows a finished write-up rendered, its source, the turn's snippets and a link to the turn", () => {
    const e = explanation({ content: "# Title\n\nSome **bold** text", message_row_id: 42, prompt: "fix   the\nbug", reply: "done it" });
    renderPane(view({ explanations: [e] }));
    const card = screen.getByTestId("explanation");
    expect(card.dataset.status).toBe("done");
    expect(within(card).getByTestId("explanation-status").textContent).toBe("done");
    expect(within(card).getByTestId("explanation-prompt").textContent).toBe("Prompt: fix the bug");
    expect(within(card).getByTestId("explanation-reply").textContent).toBe("Reply: done it");
    expect(within(card).getByTestId("explanation-goto").getAttribute("href")).toBe("#ch-1/42");
    expect(within(card).getByTestId("explanation-copy")).toBeTruthy();
    const content = within(card).getByTestId("explanation-content");
    expect(content.querySelector("h1")?.textContent).toBe("Title");
    expect(content.querySelector("strong")?.textContent).toBe("bold");
    expect(within(card).getByTestId("explanation-mode-preview").getAttribute("aria-pressed")).toBe("true");

    fireEvent.click(within(card).getByTestId("explanation-mode-source"));
    expect(within(card).getByTestId("explanation-source").textContent).toBe(e.content);
    expect(within(card).queryByTestId("explanation-content")).toBeNull();
    expect(within(card).getByTestId("explanation-mode-source").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(within(card).getByTestId("explanation-mode-preview"));
    expect(within(card).getByTestId("explanation-content")).toBeTruthy();
  });

  it("re-explains a turn on request, forcing it", () => {
    const explain = view({ explanations: [explanation({ message_id: "m-7" })] });
    renderPane(explain);
    const btn = screen.getByTestId("explanation-reexplain") as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(btn.style.cursor).toBe("pointer");
    fireEvent.click(btn);
    expect(explain.explainTurn).toHaveBeenCalledWith("m-7", true);
  });

  it.each([
    { status: "running" as const, label: "explaining…", note: "A forked session is writing up this turn…" },
    { status: "queued" as const, label: "queued", note: "Queued behind the channel's other explanations…" },
  ])("shows a $status explanation as pending, with Re-explain off", ({ status, label, note }) => {
    renderPane(view({ explanations: [explanation({ status, content: "partial" })] }));
    expect(screen.getByText("explaining a turn…")).toBeTruthy();
    expect(screen.getByTestId("explanation-status").textContent).toBe(label);
    expect(screen.getByText(note)).toBeTruthy();
    const btn = screen.getByTestId("explanation-reexplain") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.title).toBe("Being explained");
    expect(btn.style.cursor).toBe("default");
    expect(screen.queryByTestId("explanation-content")).toBeNull();
    expect(screen.queryByTestId("explanation-copy")).toBeNull();
  });

  it("shows a failed explanation's error, without snippets or a link", () => {
    renderPane(view({ explanations: [explanation({ status: "failed", error: "the run crashed" })] }));
    expect(screen.getByTestId("explanation-status").textContent).toBe("failed");
    expect(screen.getByText("the run crashed")).toBeTruthy();
    expect(screen.queryByTestId("explanation-goto")).toBeNull();
    expect(screen.queryByTestId("explanation-prompt")).toBeNull();
    expect(screen.queryByTestId("explanation-reply")).toBeNull();
  });

  it("leaves out a failed explanation's error line when it has none, and a missing snippet", () => {
    renderPane(view({ explanations: [explanation({ status: "failed", reply: "only a reply" })] }));
    const card = screen.getByTestId("explanation");
    expect(within(card).queryByTestId("explanation-prompt")).toBeNull();
    expect(within(card).getByTestId("explanation-reply")).toBeTruthy();
  });

  it("shows a card's failed request and disables Re-explain while one is in flight", () => {
    renderPane(view({ explanations: [explanation()], busy: new Set(["m-1"]), errors: new Map([["m-1", "request failed"]]) }));
    expect(screen.getByTestId("explain-request-error").textContent).toBe("request failed");
    expect((screen.getByTestId("explanation-reexplain") as HTMLButtonElement).disabled).toBe(true);
  });

  it("brings the asked-for card into view and outlines it for a while", () => {
    vi.useFakeTimers();
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const explanations = [explanation({ id: 2, message_id: "m-2" }), explanation({ id: 1, message_id: "m-1" })];
    renderPane(view({ explanations, focus: { messageId: "m-1", seq: 1 } }));
    const [other, asked] = screen.getAllByTestId("explanation");
    expect(scroll.mock.contexts[0]).toBe(asked);
    expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
    expect(asked!.dataset.highlighted).toBe("true");
    expect(other!.dataset.highlighted).toBeUndefined();
    act(() => vi.advanceTimersByTime(4000));
    expect(asked!.dataset.highlighted).toBeUndefined();
  });

  it("shows why the asked-for turn's request failed while its card isn't there", () => {
    renderPane(view({ focus: { messageId: "m-9", seq: 1 }, errors: new Map([["m-9", "can't explain"]]) }));
    expect(screen.getByTestId("explain-request-error").textContent).toBe("can't explain");
  });

  it("shows nothing for an asked-for turn without a card or an error", () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    renderPane(view({ focus: { messageId: "m-9", seq: 1 } }));
    expect(screen.queryByTestId("explain-request-error")).toBeNull();
    expect(scroll).not.toHaveBeenCalled();
  });
});
