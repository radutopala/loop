import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { TagCount } from "../../api/channels";
import { MAX_TAGS } from "../../utils/tags";
import { TagsDialog } from "./TagsDialog";

const IN_USE: TagCount[] = [
  { tag: "api", count: 3 },
  { tag: "backend", count: 1 },
  { tag: "web", count: 2 },
];

function renderDialog(currentTags = ["web"], loadTags: () => Promise<TagCount[]> = () => Promise.resolve(IN_USE)) {
  const onCancel = vi.fn();
  const onSubmit = vi.fn();
  const user = userEvent.setup();
  const view = render(<TagsDialog currentTags={currentTags} name="api" loadTags={loadTags} onCancel={onCancel} onSubmit={onSubmit} />);
  const input = screen.getByTestId("tags-input") as HTMLInputElement;
  const save = screen.getByTestId("tags-submit") as HTMLButtonElement;
  return { onCancel, onSubmit, user, input, save, view };
}

describe("TagsDialog", () => {
  it("shows the row's tags and offers the others in use, with counts", async () => {
    const { input, save } = renderDialog();
    expect(screen.getByText("Tags of api")).toBeTruthy();
    expect(screen.getByTestId("tags-chip-web")).toBeTruthy();
    expect(document.activeElement).toBe(input);
    expect(save.disabled).toBe(true);
    expect(screen.getByTestId("tags-error").textContent).toBe("Enter adds a tag; search with tag:name");
    await screen.findByTestId("tags-pick-api");
    expect(screen.getByTestId("tags-pick-api").textContent).toBe("api3");
    expect(screen.queryByTestId("tags-pick-web")).toBeNull();
  });

  it("says when there are none, and offers nothing when none are in use", async () => {
    renderDialog([], () => Promise.resolve([]));
    expect(screen.getByTestId("tags-current").textContent).toBe("No tags");
    await waitFor(() => expect(screen.queryByTestId("tags-existing")).toBeNull());
  });

  it("adds typed and picked tags, removes one, and saves the difference", async () => {
    const { input, save, user, onSubmit } = renderDialog(["web", "old"]);
    await user.type(input, "#New{Enter}");
    expect(screen.getByTestId("tags-chip-new")).toBeTruthy();
    expect(input.value).toBe("");
    // Typing narrows the tags offered.
    await user.type(input, "ap");
    await screen.findByTestId("tags-pick-api");
    expect(screen.queryByTestId("tags-pick-backend")).toBeNull();
    await user.click(screen.getByTestId("tags-pick-api"));
    await user.click(screen.getByTestId("tags-remove-old"));
    expect(save.disabled).toBe(false);
    await user.click(save);
    expect(onSubmit).toHaveBeenCalledWith(["api", "new"], ["old"]);
  });

  it("saves with Enter on an empty input, and doesn't add a tag twice", async () => {
    const { input, user, onSubmit } = renderDialog();
    await user.type(input, "web{Enter}");
    expect(screen.getAllByTestId("tags-chip-web")).toHaveLength(1);
    await user.type(input, "{Enter}");
    expect(onSubmit).not.toHaveBeenCalled();
    await user.type(input, "x{Enter}{Enter}");
    expect(onSubmit).toHaveBeenCalledWith(["x"], []);
  });

  it("refuses a tag the backend would reject", async () => {
    const { input, user } = renderDialog();
    await user.type(input, "tag:x{Enter}");
    expect(screen.getByTestId("tags-error").textContent).toBe("Only letters, digits and - _ . /, starting with a letter or digit");
    expect(screen.queryByTestId("tags-chip-tag:x")).toBeNull();
    expect(input.value).toBe("tag:x");
  });

  it("stops at the most tags a row can have", async () => {
    const many = Array.from({ length: MAX_TAGS }, (_, i) => `t${String(i).padStart(2, "0")}`);
    const { input, user } = renderDialog(many);
    const pick = (await screen.findByTestId("tags-pick-api")) as HTMLButtonElement;
    expect(pick.disabled).toBe(true);
    await user.type(input, "more{Enter}");
    expect(screen.getByTestId("tags-error").textContent).toBe(`At most ${MAX_TAGS} tags`);
    expect(screen.queryByTestId("tags-chip-more")).toBeNull();
    // A tag already on the row isn't one more.
    await user.clear(input);
    await user.type(input, "t00");
    expect(screen.getByTestId("tags-error").textContent).toBe("Enter adds a tag; search with tag:name");
  });

  it("cancels with Escape, the button or the backdrop, but not a click inside", async () => {
    const { input, user, onCancel } = renderDialog();
    await user.type(input, "{Escape}");
    expect(onCancel).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByText("Tags of api"));
    expect(onCancel).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByTestId("tags-dialog"));
    expect(onCancel).toHaveBeenCalledTimes(3);
  });

  it("logs a failed lookup", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    renderDialog(["web"], () => Promise.reject(new Error("down")));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[tags] listing tags failed:", expect.any(Error)));
    warn.mockRestore();
  });

  it("ignores a lookup that lands after closing", async () => {
    let resolve!: (t: TagCount[]) => void;
    const { view } = renderDialog(["web"], () => new Promise((r) => (resolve = r)));
    view.unmount();
    resolve(IN_USE);
    await Promise.resolve();
    expect(screen.queryByTestId("tags-dialog")).toBeNull();
  });
});
