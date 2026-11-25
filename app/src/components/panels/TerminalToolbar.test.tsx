import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TerminalToolbar } from "./TerminalToolbar";

describe("TerminalToolbar", () => {
  it("defaults to a Kill button that is enabled only while running", async () => {
    const onKill = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(<TerminalToolbar status="connecting" elapsed={0} onKill={onKill} />);
    const kill = screen.getByRole("button", { name: "Kill" }) as HTMLButtonElement;
    expect(kill.title).toBe("Kill session and remove container");
    expect(kill.disabled).toBe(true);
    expect(kill.style.cursor).toBe("default");

    rerender(<TerminalToolbar status="running" elapsed={0} onKill={onKill} />);
    expect(kill.disabled).toBe(false);
    expect(kill.style.cursor).toBe("pointer");
    await user.click(kill);
    expect(onKill).toHaveBeenCalledOnce();
  });

  it("keeps the kill button on a dead session without a restart handler", () => {
    render(<TerminalToolbar status="failed" elapsed={0} onKill={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "Restart" })).toBeNull();
    expect((screen.getByRole("button", { name: "Kill" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows only the status without handlers", () => {
    render(<TerminalToolbar status="completed" elapsed={0} />);
    expect(screen.getByText("completed")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
