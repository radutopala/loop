import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { LearnContext, type LearnView } from "../../hooks/useLearn";
import { learnPass, learnProposal, learnView } from "../../test/chat-bits-sidebar-views";
import { LearnTurnButton } from "./LearnTurnButton";

function renderButton(view: LearnView | null) {
  render(
    <LearnContext.Provider value={view}>
      <LearnTurnButton messageId="m-1" />
    </LearnContext.Provider>,
  );
  return screen.queryByTestId("learn-turn");
}

const passes = (...p: ReturnType<typeof learnPass>[]) => new Map(p.map((x) => [x.message_id, x]));

describe("LearnTurnButton", () => {
  it.each([
    ["no view", null],
    ["a view still loading", learnView({ loaded: false })],
    ["a channel that never learns", learnView({ available: false })],
  ])("renders nothing for %s", (_name, view) => {
    expect(renderButton(view)).toBeNull();
  });

  it("learns from a turn without a pass", async () => {
    const view = learnView();
    const btn = renderButton(view)!;
    expect(btn.textContent).toBe("Learn");
    expect(btn.getAttribute("data-status")).toBe("");
    expect(btn.getAttribute("data-open")).toBe("false");
    expect(btn.getAttribute("title")).toMatch(/^Learn from this turn/);
    await userEvent.click(btn);
    expect(view.learnTurn).toHaveBeenCalledWith("m-1");
    expect(view.show).not.toHaveBeenCalled();
  });

  it("treats a superseded pass as none", async () => {
    const view = learnView({ passByMessage: passes(learnPass({ status: "superseded" })) });
    const btn = renderButton(view)!;
    expect(btn.textContent).toBe("Learn");
    expect(btn.getAttribute("data-status")).toBe("");
    await userEvent.click(btn);
    expect(view.learnTurn).toHaveBeenCalledWith("m-1");
  });

  it("shows a request in flight before its pass exists as queued", async () => {
    const view = learnView({ turnBusy: new Set(["m-1"]) });
    const btn = renderButton(view)!;
    expect(btn.textContent).toBe("Learn queued…");
    expect(btn.getAttribute("data-status")).toBe("queued");
    expect(btn.getAttribute("data-open")).toBe("true");
    expect(btn.querySelector("animateTransform")).not.toBeNull();
    await userEvent.click(btn);
    expect(view.show).toHaveBeenCalledWith("m-1");
    expect(view.learnTurn).not.toHaveBeenCalled();
  });

  it.each([
    ["queued", "Learn queued…"],
    ["running", "Learning…"],
  ] as const)("follows a %s pass", (status, label) => {
    const btn = renderButton(learnView({ passByMessage: passes(learnPass({ status })) }))!;
    expect(btn.textContent).toBe(label);
    expect(btn.getAttribute("data-status")).toBe(status);
    expect(btn.getAttribute("title")).toBe("Show this turn's learn pass in the Learn view");
  });

  it("lights a done pass while one of its proposals waits", async () => {
    const view = learnView({
      passByMessage: passes(learnPass({ status: "done" })),
      proposals: [learnProposal({ id: 1, status: "pending" }), learnProposal({ id: 2, status: "applied" }), learnProposal({ id: 3, message_id: "m-2" })],
    });
    const btn = renderButton(view)!;
    expect(btn.textContent).toBe("2 proposals");
    expect(btn.getAttribute("data-open")).toBe("true");
    await userEvent.click(btn);
    expect(view.show).toHaveBeenCalledWith("m-1");
  });

  it("leaves a done pass dim once its proposals are settled", () => {
    const btn = renderButton(learnView({ passByMessage: passes(learnPass({ status: "done" })), proposals: [learnProposal({ status: "dismissed" })] }))!;
    expect(btn.textContent).toBe("1 proposal");
    expect(btn.getAttribute("data-open")).toBe("false");
  });

  it("retries a failed pass, naming its error", async () => {
    const view = learnView({ passByMessage: passes(learnPass({ status: "failed", error: "out of budget" })) });
    const btn = renderButton(view)!;
    expect(btn.textContent).toBe("Learn failed");
    expect(btn.getAttribute("title")).toBe("The learn pass failed: out of budget\nClick to try again.");
    await userEvent.click(btn);
    expect(view.learnTurn).toHaveBeenCalledWith("m-1");
  });

  it("retries a failed pass that gave no error", () => {
    const btn = renderButton(learnView({ passByMessage: passes(learnPass({ status: "failed" })) }))!;
    expect(btn.getAttribute("title")).toBe("The learn pass failed\nClick to try again.");
  });

  it("retries a request that failed", async () => {
    const view = learnView({ turnErrors: new Map([["m-1", "offline"]]) });
    const btn = renderButton(view)!;
    expect(btn.textContent).toBe("Learn failed");
    expect(btn.getAttribute("data-status")).toBe("error");
    expect(btn.getAttribute("title")).toBe("The learn pass failed: offline\nClick to try again.");
    await userEvent.click(btn);
    expect(view.learnTurn).toHaveBeenCalledWith("m-1");
  });
});
