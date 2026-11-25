import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ExplainContext, type ExplainView } from "../../hooks/useExplain";
import { explainView } from "../../test/chat-bits-sidebar-views";
import { ExplainToggle } from "./ExplainToggle";

function renderToggle(view: ExplainView | null) {
  return render(
    <ExplainContext.Provider value={view}>
      <ExplainToggle />
    </ExplainContext.Provider>,
  );
}

describe("ExplainToggle", () => {
  it.each([
    ["no view", null],
    ["a view still loading", explainView({ loaded: false })],
    ["a channel never explained", explainView({ available: false })],
  ])("renders nothing for %s", (_name, view) => {
    renderToggle(view);
    expect(screen.queryByTestId("explain-toggle")).toBeNull();
  });

  it("turns an inherited off switch on", async () => {
    const view = explainView({ explain: "", defaultExplain: false });
    renderToggle(view);
    const btn = screen.getByRole("button", { name: "Explain" });
    expect(btn.getAttribute("aria-pressed")).toBe("false");
    expect(btn.getAttribute("data-on")).toBe("false");
    await userEvent.click(btn);
    expect(view.setExplain).toHaveBeenCalledWith("on");
  });

  it("turns a channel's own on switch off and explains it on hover", async () => {
    const view = explainView({ explain: "on", defaultExplain: false });
    renderToggle(view);
    const btn = screen.getByRole("button", { name: "Explain" });
    expect(btn.getAttribute("data-on")).toBe("true");
    await userEvent.hover(btn);
    expect((await screen.findByRole("tooltip")).textContent).toMatch(/^Explain is on — set for this channel\./);
    await userEvent.click(btn);
    expect(view.setExplain).toHaveBeenCalledWith("off");
  });
});
