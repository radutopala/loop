import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mountPaneHeaderSlot } from "../../test/chat-bits-sidebar-views";
import { ShellBadge } from "./ShellBadge";

afterEach(() => {
  document.body.innerHTML = "";
});

describe("ShellBadge", () => {
  it("renders nothing without a pane header slot", () => {
    render(<ShellBadge leafId="l1" running open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("shell-badge")).toBeNull();
  });

  it("renders nothing while the container is stopped and the view closed", () => {
    mountPaneHeaderSlot("l1");
    render(<ShellBadge leafId="l1" running={false} open={false} onToggle={vi.fn()} />);
    expect(screen.queryByTestId("shell-badge")).toBeNull();
  });

  it("offers a shell while the container runs", async () => {
    mountPaneHeaderSlot("l1");
    const onToggle = vi.fn();
    render(<ShellBadge leafId="l1" running open={false} onToggle={onToggle} />);
    const badge = screen.getByTestId("shell-badge");
    expect(badge.getAttribute("title")).toBe("Open a shell in the chat's container");
    expect(badge.textContent).toBe("shell");
    await userEvent.click(badge);
    expect(onToggle).toHaveBeenCalledOnce();
  });

  it("stays to close the open view after the container stops", () => {
    mountPaneHeaderSlot("l1");
    render(<ShellBadge leafId="l1" running={false} open onToggle={vi.fn()} />);
    expect(screen.getByTestId("shell-badge").getAttribute("title")).toBe("Close the Shell view");
  });
});
