import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PromptEditsPane } from "./PromptEditsPane";

vi.mock("./LearnPane", () => ({
  HiddenThreadChat: (props: { uiChannelId?: string; threadId: string; running: boolean; readOnlyNote?: string }) => (
    <div data-testid="hidden-thread" data-ui={props.uiChannelId} data-thread={props.threadId} data-running={String(props.running)}>
      {props.readOnlyNote}
    </div>
  ),
}));

describe("PromptEditsPane", () => {
  it("says there's no edit yet before the first one", () => {
    render(<PromptEditsPane edits={{ editChannelId: "", running: false }} onClose={() => {}} />);
    expect(screen.getByTestId("edits-empty").textContent).toContain("No edit by voice yet");
    expect(screen.getByText("the composer's edits by voice")).toBeTruthy();
    expect(screen.queryByTestId("hidden-thread")).toBeNull();
  });

  it("shows the prompt-edit thread's chat, read-only, and that an edit runs", () => {
    render(<PromptEditsPane uiChannelId="ch-1" edits={{ editChannelId: "edit-1", running: true }} onClose={() => {}} />);
    const chat = screen.getByTestId("hidden-thread");
    expect(chat.dataset).toMatchObject({ ui: "ch-1", thread: "edit-1", running: "true" });
    expect(chat.textContent).toContain("Edits by voice run here");
    expect(screen.getByText("editing the draft…")).toBeTruthy();
  });

  it("closes from its button, which lights up under the mouse", () => {
    const onClose = vi.fn();
    render(<PromptEditsPane edits={{ editChannelId: "", running: false }} onClose={onClose} />);
    const btn = screen.getByRole("button", { name: "Close the Edits view" });
    fireEvent.mouseEnter(btn);
    expect(btn.style.backgroundColor).toBe("rgb(54, 54, 54)");
    fireEvent.mouseLeave(btn);
    expect(btn.style.backgroundColor).toBe("transparent");
    fireEvent.click(btn);
    expect(onClose).toHaveBeenCalledOnce();
  });
});
