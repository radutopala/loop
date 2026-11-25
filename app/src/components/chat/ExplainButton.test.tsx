import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ExplainContext, type ExplainView } from "../../hooks/useExplain";
import { explainView, explanation } from "../../test/chat-bits-sidebar-views";
import { ExplainButton } from "./ExplainButton";

function renderButton(view: ExplainView | null) {
  render(
    <ExplainContext.Provider value={view}>
      <ExplainButton messageId="m-1" />
    </ExplainContext.Provider>,
  );
  return screen.queryByTestId("explain-turn");
}

describe("ExplainButton", () => {
  it.each([
    ["no view", null],
    ["a view still loading", explainView({ loaded: false })],
    ["a channel never explained", explainView({ available: false })],
  ])("renders nothing for %s", (_name, view) => {
    expect(renderButton(view)).toBeNull();
  });

  it("explains a turn that has no explanation", async () => {
    const view = explainView();
    const btn = renderButton(view)!;
    expect(btn.getAttribute("data-state")).toBe("explain");
    expect(btn.getAttribute("data-status")).toBe("");
    expect(btn.textContent).toBe("Explain");
    expect(btn.getAttribute("title")).toMatch(/^Explain this turn/);
    await userEvent.click(btn);
    expect(view.explainTurn).toHaveBeenCalledWith("m-1");
    expect(view.show).not.toHaveBeenCalled();
  });

  it("shows a turn whose request is in flight, with a spinner", async () => {
    const view = explainView({ busy: new Set(["m-1"]) });
    const btn = renderButton(view)!;
    expect(btn.getAttribute("data-state")).toBe("pending");
    expect(btn.textContent).toBe("Explain queued…");
    expect(btn.querySelector("animateTransform")).not.toBeNull();
    await userEvent.click(btn);
    expect(view.show).toHaveBeenCalledWith("m-1");
    expect(view.explainTurn).not.toHaveBeenCalled();
  });

  it("shows a running explanation as explaining", () => {
    const btn = renderButton(explainView({ byMessage: new Map([["m-1", explanation({ status: "running" })]]) }))!;
    expect(btn.getAttribute("data-state")).toBe("pending");
    expect(btn.getAttribute("data-status")).toBe("running");
    expect(btn.textContent).toBe("Explaining…");
  });

  it("opens a done explanation in the Explain view", async () => {
    const view = explainView({ byMessage: new Map([["m-1", explanation({ status: "done" })]]) });
    const btn = renderButton(view)!;
    expect(btn.getAttribute("data-state")).toBe("open");
    expect(btn.textContent).toBe("Explained");
    expect(btn.getAttribute("title")).toBe("Show the explanation in the Explain view");
    await userEvent.click(btn);
    expect(view.show).toHaveBeenCalledWith("m-1");
  });

  it("opens a failed explanation to read why", async () => {
    const view = explainView({ byMessage: new Map([["m-1", explanation({ status: "failed" })]]) });
    const btn = renderButton(view)!;
    expect(btn.getAttribute("data-state")).toBe("open");
    expect(btn.getAttribute("data-status")).toBe("failed");
    expect(btn.textContent).toBe("Explain failed");
    await userEvent.click(btn);
    expect(view.show).toHaveBeenCalledWith("m-1");
  });

  it("retries a request that failed", async () => {
    const view = explainView({ byMessage: new Map([["m-1", explanation({ status: "done" })]]), errors: new Map([["m-1", "server down"]]) });
    const btn = renderButton(view)!;
    expect(btn.getAttribute("data-state")).toBe("error");
    expect(btn.textContent).toBe("Explain failed");
    expect(btn.getAttribute("title")).toBe("Explaining failed: server down\nClick to try again.");
    await userEvent.click(btn);
    expect(view.explainTurn).toHaveBeenCalledWith("m-1");
    expect(view.show).not.toHaveBeenCalled();
  });
});
