import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ReviewApproveDialog } from "./ReviewApproveDialog";

function deferred() {
  let resolve!: () => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function renderDialog(over: Partial<Parameters<typeof ReviewApproveDialog>[0]> = {}) {
  const props = { prNumber: 7, headSha: "abcdef1234567", initialBody: "Looks good", force: false, onCancel: vi.fn(), onSubmit: vi.fn(async () => {}), ...over };
  return { user: userEvent.setup(), props, ...render(<ReviewApproveDialog {...props} />) };
}

const body = () => screen.getByTestId("review-approve-body") as HTMLTextAreaElement;
const dialog = () => screen.getByTestId("review-approve-dialog");

describe("ReviewApproveDialog", () => {
  it("names the PR and head, focuses the text, and approves with it trimmed", async () => {
    const { user, props } = renderDialog();
    expect(screen.getByText("Approve #7 on GitHub")).toBeTruthy();
    expect(screen.getByText("abcdef1")).toBeTruthy();
    expect(document.activeElement).toBe(body());
    expect(screen.queryByTestId("review-approve-warning")).toBeNull();
    await user.type(body(), "  ");
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(props.onSubmit).toHaveBeenCalledWith("Looks good");
  });

  it("labels a forced approval and shows why it is forced", () => {
    renderDialog({ force: true, warning: "1 blocking comment left" });
    expect(screen.getByText("Approve anyway #7 on GitHub")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Approve anyway" })).toBeTruthy();
    expect(screen.getByTestId("review-approve-warning").textContent).toBe("1 blocking comment left");
  });

  it("shows no warning for a forced approval without one", () => {
    renderDialog({ force: true });
    expect(screen.queryByTestId("review-approve-warning")).toBeNull();
  });

  it.each([{ metaKey: true }, { ctrlKey: true }])("approves on Enter with %o and ignores a plain Enter", (mod) => {
    const { props } = renderDialog();
    fireEvent.keyDown(body(), { key: "Enter" });
    expect(props.onSubmit).not.toHaveBeenCalled();
    fireEvent.keyDown(body(), { key: "Enter", ...mod });
    expect(props.onSubmit).toHaveBeenCalledWith("Looks good");
  });

  it("holds while approving: no second submit, no Escape, no overlay cancel", async () => {
    const d = deferred();
    const { props } = renderDialog({ onSubmit: vi.fn(() => d.promise) });
    fireEvent.keyDown(body(), { key: "Enter", metaKey: true });
    expect(screen.getByRole("button", { name: "Approving..." })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveProperty("disabled", true);
    fireEvent.keyDown(body(), { key: "Enter", metaKey: true });
    fireEvent.keyDown(body(), { key: "Escape" });
    fireEvent.click(dialog());
    expect(props.onSubmit).toHaveBeenCalledTimes(1);
    expect(props.onCancel).not.toHaveBeenCalled();
    await act(async () => d.resolve());
  });

  it.each([
    [new Error("approval refused"), "approval refused"],
    ["gh gone", "gh gone"],
  ])("shows a failed approval (%s) and lets it be retried", async (err, text) => {
    const onSubmit = vi.fn().mockRejectedValueOnce(err).mockResolvedValue(undefined);
    const { user } = renderDialog({ onSubmit });
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(screen.getByTestId("review-approve-error").textContent).toBe(text);
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(onSubmit).toHaveBeenCalledTimes(2);
    expect(screen.queryByTestId("review-approve-error")).toBeNull();
  });

  it("cancels on Escape, Cancel and an overlay click, but not a click inside", async () => {
    const { user, props } = renderDialog();
    fireEvent.keyDown(body(), { key: "Escape" });
    expect(props.onCancel).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onCancel).toHaveBeenCalledTimes(2);
    await user.click(screen.getByText("Approve #7 on GitHub"));
    expect(props.onCancel).toHaveBeenCalledTimes(2);
    fireEvent.click(dialog());
    expect(props.onCancel).toHaveBeenCalledTimes(3);
  });

  it("edits the text before approving", async () => {
    const { user, props } = renderDialog({ initialBody: "" });
    await user.type(body(), "Ship it");
    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(props.onSubmit).toHaveBeenCalledWith("Ship it");
  });
});
