import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TicketDialog } from "./TicketDialog";

const CURRENT = "https://example.com/browse/PROJ-1";

function renderDialog(currentTicketURL = CURRENT) {
  const onCancel = vi.fn();
  const onSubmit = vi.fn();
  const user = userEvent.setup();
  render(<TicketDialog currentTicketURL={currentTicketURL} name="api" onCancel={onCancel} onSubmit={onSubmit} />);
  const input = screen.getByTestId("ticket-input") as HTMLInputElement;
  const save = screen.getByTestId("ticket-submit") as HTMLButtonElement;
  return { onCancel, onSubmit, user, input, save };
}

describe("TicketDialog", () => {
  it("prefills, focuses and selects the current URL, with the hint shown", () => {
    const { input, save } = renderDialog();
    expect(screen.getByText("Ticket of api")).toBeTruthy();
    expect(input.value).toBe(CURRENT);
    expect(document.activeElement).toBe(input);
    expect(input.selectionEnd).toBe(CURRENT.length);
    expect(screen.getByTestId("ticket-error").textContent).toBe("Jira, GitHub or any tracker's URL; empty clears it");
    expect(save.disabled).toBe(true);
  });

  it("shows why a URL can't be saved and refuses to save it", async () => {
    const { input, save, user, onSubmit } = renderDialog();
    await user.clear(input);
    await user.type(input, "not a url{Enter}");
    expect(screen.getByTestId("ticket-error").textContent).toBe("Must be an absolute http(s) URL");
    expect(save.disabled).toBe(true);
    await user.click(save);
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("saves the trimmed URL with Enter or the button", async () => {
    const { input, save, user, onSubmit } = renderDialog();
    await user.clear(input);
    await user.type(input, " https://example.com/issues/2 {Enter}");
    expect(onSubmit).toHaveBeenCalledWith("https://example.com/issues/2");
    await user.click(save);
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });

  it("clears the ticket when saved empty", async () => {
    const { input, user, onSubmit } = renderDialog();
    await user.clear(input);
    await user.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledWith("");
  });

  it("cancels on Escape, Cancel and the backdrop, but not a click inside", async () => {
    const { input, user, onCancel } = renderDialog();
    fireEvent.keyDown(input, { key: "x" });
    expect(onCancel).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Escape" });
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(input);
    expect(onCancel).toHaveBeenCalledTimes(2);
    await user.click(screen.getByTestId("ticket-dialog"));
    expect(onCancel).toHaveBeenCalledTimes(3);
  });
});
