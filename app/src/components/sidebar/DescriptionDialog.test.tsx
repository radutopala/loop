import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { DescriptionDialog, MAX_DESCRIPTION_LENGTH } from "./DescriptionDialog";

function renderDialog(currentDescription = "old") {
  const onCancel = vi.fn();
  const onSubmit = vi.fn();
  const user = userEvent.setup();
  render(<DescriptionDialog currentDescription={currentDescription} name="fix-bug" onCancel={onCancel} onSubmit={onSubmit} />);
  const input = screen.getByTestId("description-input") as HTMLTextAreaElement;
  const save = screen.getByTestId("description-submit") as HTMLButtonElement;
  return { onCancel, onSubmit, user, input, save };
}

describe("DescriptionDialog", () => {
  it("prefills, focuses and selects the current description and shows the name", () => {
    const { input } = renderDialog();
    expect(screen.getByText("Description of fix-bug")).toBeTruthy();
    expect(input.value).toBe("old");
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, 3]);
    expect(input.maxLength).toBe(MAX_DESCRIPTION_LENGTH);
    expect(screen.getByText(`3/${MAX_DESCRIPTION_LENGTH}`)).toBeTruthy();
  });

  it("can't save an unchanged description, even with extra whitespace", async () => {
    const { input, save, user, onSubmit } = renderDialog();
    expect(save.disabled).toBe(true);
    await user.type(input, "  ");
    expect(save.disabled).toBe(true);
    await user.click(save);
    fireEvent.keyDown(input, { key: "Enter" });
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("saves the trimmed description with the button", async () => {
    const { input, save, user, onSubmit } = renderDialog();
    await user.clear(input);
    await user.type(input, "  new text ");
    expect(save.disabled).toBe(false);
    await user.click(save);
    expect(onSubmit).toHaveBeenCalledWith("new text");
  });

  it("saves on Enter, keeps Shift+Enter as a newline, and clears when emptied", async () => {
    const { input, user, onSubmit } = renderDialog();
    await user.clear(input);
    await user.type(input, "a{Shift>}{Enter}{/Shift}b");
    expect(input.value).toBe("a\nb");
    expect(onSubmit).not.toHaveBeenCalled();
    await user.clear(input);
    await user.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledWith("");
  });

  it("cancels on Escape, the Cancel button and the backdrop, but not a click inside", async () => {
    const { input, user, onCancel } = renderDialog();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledTimes(2);
    await user.click(input);
    expect(onCancel).toHaveBeenCalledTimes(2);
    await user.click(screen.getByTestId("description-dialog"));
    expect(onCancel).toHaveBeenCalledTimes(3);
  });

  it("ignores other keys", () => {
    const { input, onCancel, onSubmit } = renderDialog();
    fireEvent.keyDown(input, { key: "a" });
    expect(onCancel).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});
