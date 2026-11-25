import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { explainView, explanation, mountPaneHeaderSlot } from "../../test/chat-bits-sidebar-views";
import { ExplainBadge } from "./ExplainBadge";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("ExplainBadge", () => {
  it("renders nothing without a pane header slot", () => {
    render(<ExplainBadge leafId="l1" explain={explainView({ explanations: [explanation()] })} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("explain-badge")).toBeNull();
  });

  it("renders nothing for a channel that is never explained", () => {
    mountPaneHeaderSlot("l1");
    render(<ExplainBadge leafId="l1" explain={explainView({ available: false, explanations: [explanation()] })} open onToggle={vi.fn()} />);
    expect(screen.queryByTestId("explain-badge")).toBeNull();
  });

  it("renders nothing while closed with no explanations yet", () => {
    mountPaneHeaderSlot("l1");
    render(<ExplainBadge leafId="l1" explain={explainView()} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("explain-badge")).toBeNull();
  });

  it("opens the Explain view once there are explanations", async () => {
    const slot = mountPaneHeaderSlot("l1");
    const onToggle = vi.fn();
    render(<ExplainBadge leafId="l1" explain={explainView({ explanations: [explanation()] })} open={false} onToggle={onToggle} />);
    const badge = screen.getByTestId("explain-badge");
    expect(slot.contains(badge)).toBe(true);
    expect(badge.getAttribute("title")).toBe("Open the Explain view");
    expect(badge.textContent).toBe("explain");
    await userEvent.click(badge);
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("closes the view while open, even with no explanations", () => {
    mountPaneHeaderSlot("l1");
    render(<ExplainBadge leafId="l1" explain={explainView()} open onToggle={vi.fn()} />);
    const badge = screen.getByTestId("explain-badge");
    expect(badge.getAttribute("title")).toBe("Close the Explain view");
    expect(badge.getAttribute("aria-expanded")).toBe("true");
  });
});
