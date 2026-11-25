import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ChatShellPane } from "./ChatShellPane";

vi.mock("../panels/Terminal", () => ({
  Terminal: (props: { channelId: string; target: string; cmd: string[]; uiPane?: string }) => (
    <div data-testid="terminal" data-channel={props.channelId} data-target={props.target} data-cmd={props.cmd.join(" ")} data-ui-pane={props.uiPane} />
  ),
}));

describe("ChatShellPane", () => {
  it("says the container isn't running and opens the shell once it runs, keeping it after", () => {
    const { rerender } = render(<ChatShellPane channelId="ch-1" running={false} onClose={() => {}} />);
    expect(screen.getByTestId("chat-shell-idle").textContent).toContain("isn't running");
    expect(screen.queryByTestId("terminal")).toBeNull();

    rerender(<ChatShellPane channelId="ch-1" running uiPane="side" onClose={() => {}} />);
    const term = screen.getByTestId("terminal");
    expect(term.dataset).toMatchObject({ channel: "ch-1", target: "chat", cmd: "/bin/bash", uiPane: "side" });

    rerender(<ChatShellPane channelId="ch-1" running={false} uiPane="side" onClose={() => {}} />);
    expect(screen.getByTestId("terminal")).toBeTruthy();
    expect(screen.queryByTestId("chat-shell-idle")).toBeNull();
  });

  it("closes from its button, which lights up under the mouse", () => {
    const onClose = vi.fn();
    render(<ChatShellPane channelId="ch-1" running onClose={onClose} />);
    const btn = screen.getByRole("button", { name: "Close the Shell view" });
    fireEvent.mouseEnter(btn);
    expect(btn.style.backgroundColor).toBe("rgb(54, 54, 54)");
    expect(btn.style.color).toBe("rgb(236, 236, 236)");
    fireEvent.mouseLeave(btn);
    expect(btn.style.backgroundColor).toBe("transparent");
    fireEvent.click(btn);
    expect(onClose).toHaveBeenCalledOnce();
  });
});
