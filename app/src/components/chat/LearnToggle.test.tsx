import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { LearnContext, type LearnView } from "../../hooks/useLearn";
import { learnView } from "../../test/chat-bits-sidebar-views";
import { LearnToggle } from "./LearnToggle";

function renderToggle(view: LearnView | null) {
  return render(
    <LearnContext.Provider value={view}>
      <LearnToggle />
    </LearnContext.Provider>,
  );
}

describe("LearnToggle", () => {
  it.each([
    ["no view", null],
    ["a view still loading", learnView({ loaded: false })],
    ["a channel that never learns", learnView({ available: false })],
  ])("renders nothing for %s", (_name, view) => {
    renderToggle(view);
    expect(screen.queryByTestId("learn-toggle")).toBeNull();
  });

  it("turns an inherited on switch off", async () => {
    const view = learnView({ learn: "", defaultLearn: true });
    renderToggle(view);
    const btn = screen.getByRole("button", { name: "Learn" });
    expect(btn.getAttribute("aria-pressed")).toBe("true");
    expect(btn.getAttribute("data-on")).toBe("true");
    await userEvent.click(btn);
    expect(view.setLearn).toHaveBeenCalledWith("off");
  });

  it("turns a channel's own off switch on", async () => {
    const view = learnView({ learn: "off", defaultLearn: true });
    renderToggle(view);
    const btn = screen.getByRole("button", { name: "Learn" });
    expect(btn.getAttribute("data-on")).toBe("false");
    await userEvent.click(btn);
    expect(view.setLearn).toHaveBeenCalledWith("on");
  });
});
