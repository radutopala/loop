import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GateApprovalGoneError } from "../../api/gate";
import { resolveGateApproval, sendCommand, sendMessage } from "../../api/loopApi";
import type { GateApprovalRequestedData } from "../../types";
import { ApprovalCard } from "./ApprovalCard";

vi.mock("../../api/loopApi", () => ({
  resolveGateApproval: vi.fn(),
  sendCommand: vi.fn(),
  sendMessage: vi.fn(),
}));

const resolveMock = vi.mocked(resolveGateApproval);
const commandMock = vi.mocked(sendCommand);
const messageMock = vi.mocked(sendMessage);

const request: GateApprovalRequestedData = {
  req_id: "req-1",
  kind: "network",
  target: "api.example.com:443",
  message: "The agent wants to reach a host outside the allowlist.",
  details: { method: "CONNECT", by: "curl" },
};

function renderCard(props: Partial<Parameters<typeof ApprovalCard>[0]> = {}) {
  const onResolved = vi.fn();
  const user = userEvent.setup();
  render(<ApprovalCard data={request} channelId="ch-1" onResolved={onResolved} {...props} />);
  return { onResolved, user };
}

describe("ApprovalCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resolveMock.mockResolvedValue(undefined);
    commandMock.mockResolvedValue(undefined as never);
    messageMock.mockResolvedValue(undefined as never);
  });

  it("shows the request's kind, target, message and sorted details", () => {
    renderCard();
    expect(screen.getByText("Gate · NETWORK")).toBeTruthy();
    expect(screen.getByText("api.example.com:443")).toBeTruthy();
    expect(screen.getByText(request.message!)).toBeTruthy();
    const keys = [...document.querySelectorAll("span")].map((s) => s.textContent).filter((t) => t === "by" || t === "method");
    expect(keys).toEqual(["by", "method"]);
  });

  it("labels a request without a kind as an approval", () => {
    renderCard({ data: { ...request, kind: "" } });
    expect(screen.getByText("Gate · APPROVAL")).toBeTruthy();
  });

  it.each([
    ["Allow once", "once"],
    ["Allow for session", "session"],
    ["Deny", "deny"],
  ] as const)("%s resolves the request as %s", async (label, decision) => {
    const { onResolved, user } = renderCard();
    await user.click(screen.getByRole("button", { name: label }));
    expect(resolveMock).toHaveBeenCalledWith("req-1", decision);
    await waitFor(() => expect(onResolved).toHaveBeenCalledOnce());
  });

  it("disables every decision while one is in flight", async () => {
    let finish!: () => void;
    resolveMock.mockReturnValue(new Promise<void>((r) => (finish = r)));
    const { user } = renderCard();
    await user.click(screen.getByRole("button", { name: "Allow once" }));
    expect(screen.getByRole("button", { name: "..." })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "Allow for session" })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: "More deny options" })).toHaveProperty("disabled", true);
    await act(async () => finish());
  });

  it("shows a failed decision's error and lets the user dismiss the card", async () => {
    resolveMock.mockRejectedValue(new Error("daemon unreachable"));
    const { onResolved, user } = renderCard();
    await user.click(screen.getByRole("button", { name: "Allow once" }));
    expect(await screen.findByText("daemon unreachable")).toBeTruthy();
    expect(onResolved).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Allow once" })).toHaveProperty("disabled", false);
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(onResolved).toHaveBeenCalledOnce();
  });

  it("switches to the expired state when the request is already gone", async () => {
    resolveMock.mockRejectedValue(new GateApprovalGoneError());
    const { onResolved, user } = renderCard();
    await user.click(screen.getByRole("button", { name: "Deny" }));
    expect(await screen.findByTestId("approval-expired")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Allow once" })).toBeNull();
    expect(onResolved).not.toHaveBeenCalled();
  });

  it("shows a non-Error rejection as text", async () => {
    resolveMock.mockRejectedValue("gate said no");
    const { user } = renderCard();
    await user.click(screen.getByRole("button", { name: "Allow once" }));
    expect(await screen.findByText("gate said no")).toBeTruthy();
  });

  it("skips the details box without details and renders a diff detail as a diff", () => {
    const { unmount } = render(<ApprovalCard data={{ req_id: "r", kind: "git", target: "push" }} channelId="ch-1" />);
    expect(screen.queryByText("method")).toBeNull();
    expect(screen.queryByTestId("approval-diff")).toBeNull();
    unmount();
    render(<ApprovalCard data={{ ...request, details: { diff: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new", ref: "main" } }} channelId="ch-1" />);
    expect(screen.getByTestId("approval-diff")).toBeTruthy();
    expect(screen.getByText("ref")).toBeTruthy();
    expect(screen.queryByText("diff")).toBeNull();
  });

  describe("scrolling into view", () => {
    it("jumps the nearest scrollable ancestor to its bottom", async () => {
      render(
        <div data-testid="scroller" style={{ overflowY: "auto" }}>
          <div>
            <ApprovalCard data={request} channelId="ch-1" />
          </div>
        </div>,
      );
      const scroller = screen.getByTestId("scroller");
      Object.defineProperty(scroller, "scrollHeight", { configurable: true, value: 900 });
      await waitFor(() => expect(scroller.scrollTop).toBe(900));
    });

    it("scrolls the card itself into view without a scrollable ancestor", async () => {
      const spy = vi.spyOn(HTMLElement.prototype, "scrollIntoView");
      renderCard();
      await waitFor(() => expect(spy).toHaveBeenCalledWith({ block: "end" }));
      spy.mockRestore();
    });
  });

  describe("deny menu", () => {
    // The menu stays open when a decision starts from a plain click (no
    // mousedown on the backdrop), so each entry has to check for one in flight.
    it.each(["Deny for session", "Deny & stop run", "Deny with prompt…"])("ignores %s while a decision is in flight", async (item) => {
      resolveMock.mockReturnValue(new Promise<void>(() => {}));
      const { user } = renderCard();
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
      expect(resolveMock).toHaveBeenCalledTimes(1);
      fireEvent.click(screen.getByRole("menuitem", { name: item }));
      expect(resolveMock).toHaveBeenCalledTimes(1);
      expect(commandMock).not.toHaveBeenCalled();
      expect(screen.queryByPlaceholderText(/Tell the agent what to do instead/)).toBeNull();
    });

    it("switches to the expired state when deny-and-stop finds the request gone", async () => {
      resolveMock.mockRejectedValue(new GateApprovalGoneError());
      const { onResolved, user } = renderCard();
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny & stop run" }));
      expect(await screen.findByTestId("approval-expired")).toBeTruthy();
      expect(commandMock).not.toHaveBeenCalled();
      expect(onResolved).not.toHaveBeenCalled();
    });

    it("shows a non-Error failure of deny-and-stop as text", async () => {
      commandMock.mockRejectedValue("stop refused");
      const { onResolved, user } = renderCard();
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny & stop run" }));
      expect(await screen.findByText("stop refused")).toBeTruthy();
      expect(onResolved).not.toHaveBeenCalled();
    });

    it("denies for the session", async () => {
      const { onResolved, user } = renderCard();
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny for session" }));
      expect(resolveMock).toHaveBeenCalledWith("req-1", "deny-session");
      await waitFor(() => expect(onResolved).toHaveBeenCalledOnce());
    });

    it("denies, then stops the run", async () => {
      const { onResolved, user } = renderCard();
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny & stop run" }));
      await waitFor(() => expect(onResolved).toHaveBeenCalledOnce());
      expect(resolveMock).toHaveBeenCalledWith("req-1", "deny");
      expect(commandMock).toHaveBeenCalledWith("ch-1", "stop");
      expect(resolveMock.mock.invocationCallOrder[0]).toBeLessThan(commandMock.mock.invocationCallOrder[0]!);
    });

    it("doesn't stop the run when the deny fails", async () => {
      resolveMock.mockRejectedValue(new Error("boom"));
      const { user } = renderCard();
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny & stop run" }));
      expect(await screen.findByText("boom")).toBeTruthy();
      expect(commandMock).not.toHaveBeenCalled();
    });

    it("has no stop-run option on a terminal pane's card", async () => {
      const { user } = renderCard({ onDenyWithPrompt: vi.fn() });
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      expect(screen.queryByRole("menuitem", { name: "Deny & stop run" })).toBeNull();
      expect(screen.getByRole("menuitem", { name: "Deny for session" })).toBeTruthy();
    });
  });

  describe("deny with prompt", () => {
    const openPrompt = async (user: ReturnType<typeof userEvent.setup>) => {
      await user.click(screen.getByRole("button", { name: "More deny options" }));
      await user.click(screen.getByRole("menuitem", { name: "Deny with prompt…" }));
      return screen.getByPlaceholderText(/Tell the agent what to do instead/);
    };

    it("denies and steers the chat with the typed prompt on Enter", async () => {
      const { onResolved, user } = renderCard();
      const box = await openPrompt(user);
      expect(screen.getByRole("button", { name: "Deny & send prompt" })).toHaveProperty("disabled", true);
      await user.type(box, "  use the mirror instead {Enter}");
      await waitFor(() => expect(onResolved).toHaveBeenCalledOnce());
      expect(resolveMock).toHaveBeenCalledWith("req-1", "deny");
      expect(messageMock).toHaveBeenCalledWith("ch-1", "use the mirror instead", undefined, "steer");
    });

    it("keeps Shift+Enter as a newline and ignores a blank prompt", async () => {
      const { user } = renderCard();
      const box = await openPrompt(user);
      await user.type(box, "{Shift>}{Enter}{/Shift}{Enter}");
      expect(resolveMock).not.toHaveBeenCalled();
      expect((box as HTMLTextAreaElement).value).toBe("\n");
    });

    it("hands the prompt to a terminal pane instead of posting it", async () => {
      const onDenyWithPrompt = vi.fn();
      const { onResolved, user } = renderCard({ onDenyWithPrompt });
      const box = await openPrompt(user);
      await user.type(box, "try again");
      await user.click(screen.getByRole("button", { name: "Deny & send prompt" }));
      await waitFor(() => expect(onResolved).toHaveBeenCalledOnce());
      expect(onDenyWithPrompt).toHaveBeenCalledWith("try again");
      expect(messageMock).not.toHaveBeenCalled();
    });

    it.each([
      ["an Error", new Error("send failed"), "send failed"],
      ["a non-Error", "queue full", "queue full"],
    ])("shows %s from a failed send and keeps the prompt open", async (_name, err, text) => {
      messageMock.mockRejectedValue(err);
      const { onResolved, user } = renderCard();
      const box = await openPrompt(user);
      await user.type(box, "retry{Enter}");
      expect(await screen.findByText(text)).toBeTruthy();
      expect(onResolved).not.toHaveBeenCalled();
      expect(screen.getByRole("button", { name: "Deny & send prompt" })).toHaveProperty("disabled", false);
    });

    it("closes and clears on Cancel", async () => {
      const { user } = renderCard();
      const box = await openPrompt(user);
      await user.type(box, "draft");
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      expect(screen.queryByPlaceholderText(/Tell the agent what to do instead/)).toBeNull();
      const reopened = await openPrompt(user);
      expect((reopened as HTMLTextAreaElement).value).toBe("");
    });
  });

  describe("expiry", () => {
    beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
    afterEach(() => vi.useRealTimers());

    it("greys out at the deadline, then retracts 15s later", () => {
      const onResolved = vi.fn();
      const expiresAt = new Date(Date.now() + 5_000).toISOString();
      render(<ApprovalCard data={{ ...request, expires_at: expiresAt }} channelId="ch-1" onResolved={onResolved} />);
      expect(screen.queryByTestId("approval-expired")).toBeNull();
      act(() => vi.advanceTimersByTime(5_000));
      expect(screen.getByTestId("approval-expired")).toBeTruthy();
      expect(onResolved).not.toHaveBeenCalled();
      act(() => vi.advanceTimersByTime(15_000));
      expect(onResolved).toHaveBeenCalledOnce();
    });

    it("renders expired straight away when the deadline has passed", () => {
      render(<ApprovalCard data={{ ...request, expires_at: new Date(Date.now() - 1_000).toISOString() }} channelId="ch-1" />);
      expect(screen.getByTestId("approval-expired")).toBeTruthy();
    });
  });
});
