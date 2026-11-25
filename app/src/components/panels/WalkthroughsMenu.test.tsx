import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deleteWalkthrough, fetchWalkthrough, listWalkthroughs, type Walkthrough } from "../../api/walkthroughs";
import { closeWalkthrough, type OpenWalkthrough, showWalkthrough } from "../../hooks/walkthroughStore";
import { openChannelFile } from "../../utils/openChannelFile";
import { WalkthroughsBadge } from "./WalkthroughsMenu";

vi.mock("../../api/walkthroughs", () => ({
  deleteWalkthrough: vi.fn(),
  fetchWalkthrough: vi.fn(),
  listWalkthroughs: vi.fn(),
}));

// Showing a walkthrough opens files in the editor; the mocks record the calls.
vi.mock("../../hooks/walkthroughStore", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/walkthroughStore")>()),
  closeWalkthrough: vi.fn(),
  showWalkthrough: vi.fn(),
}));

vi.mock("../../utils/openChannelFile", () => ({ openChannelFile: vi.fn() }));

const step = (path: string) => ({ path, start_line: 1, end_line: 2 });

const tourA: Walkthrough = { id: 1, channel_id: "c", title: "Tour A", created_at: new Date(Date.now() - 5 * 60_000).toISOString(), step_count: 3 };
const tourB: Walkthrough = { id: 2, channel_id: "c", title: "Tour B", created_at: new Date().toISOString(), step_count: 1 };

const openA: OpenWalkthrough = { id: 1, title: "Tour A", steps: [step("a.go"), step("b.go")], step: 1, target: {} as OpenWalkthrough["target"] };

let slot: HTMLDivElement;

function addSlot() {
  slot = document.createElement("div");
  slot.id = "pane-header-slot-leaf";
  document.body.appendChild(slot);
}

function renderBadge(open: OpenWalkthrough | null = null) {
  return render(<WalkthroughsBadge leafId="leaf" channelId="c" open={open} />);
}

const badge = () => screen.getByTestId("walkthroughs-badge");
const menu = () => screen.getByTestId("walkthroughs-menu");
const items = () => screen.getAllByTestId("walkthroughs-menu-item");

beforeEach(() => {
  addSlot();
  vi.mocked(listWalkthroughs).mockResolvedValue([tourA, tourB]);
  vi.mocked(deleteWalkthrough).mockResolvedValue(undefined as never);
  vi.mocked(showWalkthrough).mockResolvedValue(undefined);
});

afterEach(() => {
  slot.remove();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("WalkthroughsBadge", () => {
  it("renders nothing without a header slot", async () => {
    slot.remove();
    renderBadge(openA);
    await waitFor(() => expect(listWalkthroughs).toHaveBeenCalledWith("c"));
    expect(screen.queryByTestId("walkthroughs-badge")).toBeNull();
  });

  it("renders nothing while the channel has no walkthroughs and none is open", async () => {
    vi.mocked(listWalkthroughs).mockResolvedValue([]);
    renderBadge();
    await waitFor(() => expect(listWalkthroughs).toHaveBeenCalled());
    await act(async () => {});
    expect(screen.queryByTestId("walkthroughs-badge")).toBeNull();
  });

  it("labels itself in the slot and lists walkthroughs newest first", async () => {
    const user = userEvent.setup();
    renderBadge();
    const b = await screen.findByTestId("walkthroughs-badge");
    expect(slot.contains(b)).toBe(true);
    expect(b.textContent).toBe("walkthroughs");
    expect(b.title).toBe("Walkthroughs of this channel");
    expect(b.getAttribute("aria-expanded")).toBe("false");
    expect(b.style.background).toBe("transparent");

    vi.spyOn(b, "getBoundingClientRect").mockReturnValue({ right: 500, bottom: 20 } as DOMRect);
    await user.click(b);
    expect(listWalkthroughs).toHaveBeenCalledTimes(2);
    expect(b.getAttribute("aria-expanded")).toBe("true");
    expect(menu().style.top).toBe("24px");
    expect(menu().style.left).toBe("200px");
    const [a, bb] = items();
    expect(a!.textContent).toContain("Tour A");
    expect(a!.textContent).toContain("3 steps · 5m ago");
    expect(within(a!).getByTitle("Open from its first step")).toBeTruthy();
    expect(bb!.textContent).toContain("1 step · 0m ago");

    // Pressing the badge from the keyboard while the menu is open closes it.
    b.focus();
    await user.keyboard("{Enter}");
    expect(screen.queryByTestId("walkthroughs-menu")).toBeNull();
  });

  it("keeps the menu on screen near the left edge", async () => {
    const user = userEvent.setup();
    renderBadge();
    const b = await screen.findByTestId("walkthroughs-badge");
    vi.spyOn(b, "getBoundingClientRect").mockReturnValue({ right: 100, bottom: 0 } as DOMRect);
    await user.click(b);
    expect(menu().style.left).toBe("8px");
  });

  it("closes on an outside click or Escape, not on clicks inside or other keys", async () => {
    const user = userEvent.setup();
    renderBadge();
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    fireEvent.mouseDown(items()[0]!);
    fireEvent.keyDown(document, { key: "a" });
    expect(menu()).toBeTruthy();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByTestId("walkthroughs-menu")).toBeNull();

    await user.click(badge());
    fireEvent.mouseDown(document.body);
    expect(screen.queryByTestId("walkthroughs-menu")).toBeNull();
  });

  it("highlights items and the delete button on hover", async () => {
    const user = userEvent.setup();
    renderBadge(openA);
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    const [a, b] = items();
    const openBg = a!.style.backgroundColor;
    expect(openBg).not.toBe("");
    fireEvent.mouseEnter(a!);
    fireEvent.mouseLeave(a!);
    expect(a!.style.backgroundColor).toBe(openBg);

    fireEvent.mouseEnter(b!);
    expect(b!.style.backgroundColor).not.toBe("");
    fireEvent.mouseLeave(b!);
    expect(b!.style.backgroundColor).toBe("");

    const del = screen.getByRole("button", { name: "Delete Tour B" });
    fireEvent.mouseEnter(del);
    const hover = del.style.color;
    fireEvent.mouseLeave(del);
    expect(del.style.color).not.toBe(hover);
  });

  it("shows the open walkthrough's step and returns to it", async () => {
    const user = userEvent.setup();
    renderBadge(openA);
    const b = await screen.findByTestId("walkthroughs-badge");
    expect(b.textContent).toBe("walkthrough 2/2");
    expect(b.title).toBe("Tour A: step 2 of 2");
    expect(b.style.background).not.toBe("transparent");
    await user.click(b);
    const a = items()[0]!;
    expect(a.textContent).toContain("step 2 of 2 · ");
    await user.click(within(a).getByTitle("Back to the step it shows"));
    expect(showWalkthrough).toHaveBeenCalledWith("c", openA, 1, openChannelFile);
    expect(fetchWalkthrough).not.toHaveBeenCalled();
    expect(screen.queryByTestId("walkthroughs-menu")).toBeNull();
  });

  it("opens another walkthrough from its first step", async () => {
    const user = userEvent.setup();
    const steps = [step("x.go")];
    vi.mocked(fetchWalkthrough).mockResolvedValue({ ...tourB, steps });
    renderBadge(openA);
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    await user.click(within(items()[1]!).getByTitle("Open from its first step"));
    expect(fetchWalkthrough).toHaveBeenCalledWith("c", 2);
    expect(showWalkthrough).toHaveBeenCalledWith("c", { id: 2, title: "Tour B", steps }, 0, openChannelFile);
  });

  // The menu closes when a walkthrough is picked; reopening it shows the
  // error until the list refresh lands, which here never does.
  async function errorAfterReopen(user: ReturnType<typeof userEvent.setup>) {
    vi.mocked(listWalkthroughs).mockReturnValueOnce(new Promise(() => {}));
    await user.click(badge());
    return menu().textContent;
  }

  it.each([
    ["has no steps", { ...tourB, steps: [] }, 'walkthrough "Tour B" has no steps'],
    ["is missing", null, 'walkthrough "Tour B" has no steps'],
  ])("reports a walkthrough that %s", async (_name, full, message) => {
    const user = userEvent.setup();
    vi.mocked(fetchWalkthrough).mockResolvedValue(full as never);
    renderBadge();
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    await user.click(within(items()[1]!).getByTitle("Open from its first step"));
    expect(showWalkthrough).not.toHaveBeenCalled();
    expect(await errorAfterReopen(user)).toContain(message);
  });

  it("reports a non-Error failure to open", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWalkthrough).mockRejectedValue("offline");
    renderBadge();
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    await user.click(within(items()[0]!).getByTitle("Open from its first step"));
    expect(await errorAfterReopen(user)).toContain("offline");
  });

  it("deletes a walkthrough and closes it when it is the open one", async () => {
    const user = userEvent.setup();
    renderBadge(openA);
    await user.click(await screen.findByTestId("walkthroughs-badge"));

    await user.click(screen.getByRole("button", { name: "Delete Tour B" }));
    expect(deleteWalkthrough).toHaveBeenCalledWith("c", 2);
    expect(closeWalkthrough).not.toHaveBeenCalled();
    expect(items()).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Delete Tour A" }));
    expect(deleteWalkthrough).toHaveBeenCalledWith("c", 1);
    expect(closeWalkthrough).toHaveBeenCalledWith("c");
    // The open walkthrough keeps the badge up with an empty list.
    expect(menu().textContent).toBe("No walkthroughs");
  });

  it.each([
    ["an Error", new Error("forbidden"), "forbidden"],
    ["a string", "nope", "nope"],
  ])("shows a delete failure given as %s", async (_name, err, message) => {
    const user = userEvent.setup();
    vi.mocked(deleteWalkthrough).mockRejectedValue(err);
    renderBadge();
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    await user.click(screen.getByRole("button", { name: "Delete Tour A" }));
    expect(menu().textContent).toContain(message);
    expect(items()).toHaveLength(2);
  });

  it("shows a failed list load while a walkthrough is open, and clears it on success", async () => {
    const user = userEvent.setup();
    vi.mocked(listWalkthroughs).mockRejectedValueOnce(new Error("down"));
    vi.mocked(listWalkthroughs).mockReturnValueOnce(new Promise(() => {}));
    const { rerender } = renderBadge(openA);
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    expect(menu().textContent).toBe("down");
    expect(screen.queryAllByTestId("walkthroughs-menu-item")).toHaveLength(0);

    // A different open walkthrough triggers a refresh.
    rerender(<WalkthroughsBadge leafId="leaf" channelId="c" open={{ ...openA, id: 2, title: "Tour B" }} />);
    await waitFor(() => expect(items()).toHaveLength(2));
    expect(menu().textContent).not.toContain("down");
    expect(listWalkthroughs).toHaveBeenCalledTimes(3);
  });

  it("shows a non-Error list failure", async () => {
    const user = userEvent.setup();
    vi.mocked(listWalkthroughs).mockRejectedValueOnce("boom");
    vi.mocked(listWalkthroughs).mockReturnValueOnce(new Promise(() => {}));
    renderBadge(openA);
    await user.click(await screen.findByTestId("walkthroughs-badge"));
    await waitFor(() => expect(menu().textContent).toBe("boom"));
  });
});
