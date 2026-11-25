import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mountPaneHeaderSlot } from "../../test/chat-bits-sidebar-views";
import { PromptEditsBadge } from "./PromptEditsBadge";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("PromptEditsBadge", () => {
  it("renders nothing without a pane header slot", () => {
    render(<PromptEditsBadge leafId="l1" edits={{ editChannelId: "e1", running: false }} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("edits-badge")).toBeNull();
  });

  it("renders nothing before the channel's first edit while closed", () => {
    mountPaneHeaderSlot("l1");
    render(<PromptEditsBadge leafId="l1" edits={{ editChannelId: "", running: false }} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("edits-badge")).toBeNull();
  });

  it("opens the Edits view once there is an edit thread", async () => {
    mountPaneHeaderSlot("l1");
    const onToggle = vi.fn();
    render(<PromptEditsBadge leafId="l1" edits={{ editChannelId: "e1", running: false }} open={false} onToggle={onToggle} />);
    const badge = screen.getByTestId("edits-badge");
    expect(badge.getAttribute("title")).toBe("Open the Edits view: the chat of the edits by voice");
    expect(badge.textContent).toBe("edits");
    await userEvent.click(badge);
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("closes the open view", () => {
    mountPaneHeaderSlot("l1");
    render(<PromptEditsBadge leafId="l1" edits={{ editChannelId: "", running: false }} open onToggle={vi.fn()} />);
    expect(screen.getByTestId("edits-badge").getAttribute("title")).toBe("Close the Edits view");
  });
});
