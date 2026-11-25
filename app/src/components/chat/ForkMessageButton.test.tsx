import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ForkMessageButton } from "./ForkMessageButton";

describe("ForkMessageButton", () => {
  it("is hidden until its bubble is hovered", () => {
    const { rerender } = render(<ForkMessageButton onFork={vi.fn()} visible={false} />);
    const btn = screen.getByTestId("fork-message-btn");
    expect(btn.style.opacity).toBe("0");
    rerender(<ForkMessageButton onFork={vi.fn()} visible style={{ marginTop: 2 }} />);
    expect(btn.style.opacity).toBe("1");
    expect(btn.style.marginTop).toBe("2px");
  });

  it("forks once while the fork runs, showing it pending", async () => {
    let finish!: () => void;
    const onFork = vi.fn(() => new Promise<void>((r) => (finish = r)));
    render(<ForkMessageButton onFork={onFork} visible={false} />);
    const btn = screen.getByTestId("fork-message-btn");
    await userEvent.click(btn);
    expect(btn.getAttribute("data-state")).toBe("pending");
    expect(btn.style.opacity).toBe("1");
    expect(btn.querySelector("animateTransform")).not.toBeNull();
    await userEvent.click(btn);
    expect(onFork).toHaveBeenCalledOnce();
    await act(async () => finish());
    expect(btn.getAttribute("data-state")).toBe("");
    expect(btn.textContent).toBe("+fork");
  });

  it.each([
    ["an Error", new Error("no session"), "no session"],
    ["anything else", "gone", "gone"],
  ])("shows a fork that failed with %s and tries again on click", async (_name, err, msg) => {
    const onFork = vi.fn().mockRejectedValueOnce(err).mockResolvedValueOnce(undefined);
    render(<ForkMessageButton onFork={onFork} visible={false} />);
    const btn = screen.getByTestId("fork-message-btn");
    await userEvent.click(btn);
    expect(btn.getAttribute("data-state")).toBe("error");
    expect(btn.textContent).toBe("Fork failed");
    expect(btn.getAttribute("title")).toBe(`Forking failed: ${msg}\nClick to try again.`);
    expect(btn.style.opacity).toBe("1");
    await userEvent.click(btn);
    expect(onFork).toHaveBeenCalledTimes(2);
    expect(btn.getAttribute("data-state")).toBe("");
  });

  it("lights up on hover unless it failed", async () => {
    const onFork = vi.fn().mockRejectedValue(new Error("x"));
    render(<ForkMessageButton onFork={onFork} visible />);
    const btn = screen.getByTestId("fork-message-btn");
    const dim = btn.style.color;
    await userEvent.hover(btn);
    const lit = btn.style.color;
    expect(lit).not.toBe(dim);
    await userEvent.unhover(btn);
    expect(btn.style.color).toBe(dim);
    await userEvent.click(btn);
    const failed = btn.style.color;
    await userEvent.hover(btn);
    expect(btn.style.color).toBe(failed);
    await userEvent.unhover(btn);
    expect(btn.style.color).toBe(failed);
  });
});
