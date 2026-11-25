import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { NewThreadInput } from "./NewThreadInput";

function renderInput() {
  const onSubmit = vi.fn();
  const onCancel = vi.fn();
  render(<NewThreadInput onSubmit={onSubmit} onCancel={onCancel} />);
  return { onSubmit, onCancel, input: screen.getByPlaceholderText("Thread name...") as HTMLInputElement, user: userEvent.setup() };
}

describe("NewThreadInput", () => {
  it("is focused and submits the trimmed name on Enter", async () => {
    const { input, onSubmit, user } = renderInput();
    expect(document.activeElement).toBe(input);
    await user.type(input, "  my thread {Enter}");
    expect(onSubmit).toHaveBeenCalledWith("my thread");
  });

  it("ignores Enter on a blank name and other keys", async () => {
    const { input, onSubmit, onCancel, user } = renderInput();
    await user.type(input, "   {Enter}");
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("cancels on Escape, preventing the default", () => {
    const { input, onCancel } = renderInput();
    const ev = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    input.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(onCancel).toHaveBeenCalledOnce();
  });

  it("cancels on blur", () => {
    const { input, onCancel } = renderInput();
    fireEvent.blur(input);
    expect(onCancel).toHaveBeenCalledOnce();
  });
});
