import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { RenameThreadDialog } from "./RenameThreadDialog";

function renderDialog(isWorktree = false) {
  const onCancel = vi.fn();
  const onSubmit = vi.fn();
  const user = userEvent.setup();
  render(<RenameThreadDialog currentName="old-name" isWorktree={isWorktree} onCancel={onCancel} onSubmit={onSubmit} />);
  const input = screen.getByTestId("rename-thread-input") as HTMLInputElement;
  const submit = screen.getByTestId("rename-thread-submit") as HTMLButtonElement;
  return { onCancel, onSubmit, user, input, submit };
}

describe("RenameThreadDialog", () => {
  it.each([
    [false, "Rename Thread"],
    [true, "Rename Worktree"],
  ])("titles a worktree=%s rename as %s", (isWorktree, title) => {
    renderDialog(isWorktree);
    expect(screen.getByText(title)).toBeTruthy();
  });

  it("prefills, focuses and selects the current name", () => {
    const { input } = renderDialog();
    expect(input.value).toBe("old-name");
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, 8]);
  });

  it("can't rename to the same name or a blank one", async () => {
    const { input, submit, user, onSubmit } = renderDialog();
    expect(submit.disabled).toBe(true);
    await user.clear(input);
    await user.type(input, "   ");
    expect(submit.disabled).toBe(true);
    await user.keyboard("{Enter}");
    await user.click(submit);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("renames to the trimmed name with Enter or the button", async () => {
    const { input, submit, user, onSubmit } = renderDialog();
    await user.clear(input);
    await user.type(input, " new-name {Enter}");
    expect(onSubmit).toHaveBeenCalledWith("new-name");
    await user.click(submit);
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });

  it("cancels on Escape, Cancel and the backdrop, but not a click inside", async () => {
    const { input, user, onCancel, onSubmit } = renderDialog();
    fireEvent.keyDown(input, { key: "x" });
    expect(onCancel).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Escape" });
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(input);
    expect(onCancel).toHaveBeenCalledTimes(2);
    await user.click(screen.getByTestId("rename-thread-dialog"));
    expect(onCancel).toHaveBeenCalledTimes(3);
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
