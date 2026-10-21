import { describe, expect, it } from "vitest";
import type { Explanation } from "../api/explain";
import type { LearnProposal } from "../api/learn";
import { explainText, learnText } from "./sideText";

const explanation = (e: Partial<Explanation>): Explanation => ({
  id: 1,
  channel_id: "c1",
  message_id: "m1",
  explain_channel_id: "x1",
  status: "done",
  content: "",
  created_at: "",
  updated_at: "",
  ...e,
});

const proposal = (p: Partial<LearnProposal>): LearnProposal => ({
  id: 1,
  channel_id: "c1",
  learn_channel_id: "l1",
  kind: "bash_shortcut",
  title: "",
  rationale: "",
  payload: "{}",
  status: "pending",
  created_at: "",
  updated_at: "",
  ...p,
});

describe("explainText", () => {
  it("reads the explained turns oldest first", () => {
    const newest = explanation({ message_id: "m2", message_row_id: 9, prompt: "why\nmore", content: "Because." });
    const oldest = explanation({ message_id: "m1", status: "failed", error: "timed out" });
    const failedBare = explanation({ message_id: "m0", status: "failed" });
    expect(explainText([newest, oldest, failedBare], 50)).toBe("## turn m0 (failed)\nerror: failed\n\n## turn m1 (failed)\nerror: timed out\n\n## turn #9 (done)\nprompt: why\nBecause.");
  });

  it("reads its last lines", () => {
    expect(explainText([explanation({ content: "a\nb\nc" })], 2)).toBe("b\nc");
  });

  it("says when there's nothing yet", () => {
    expect(explainText([], 50)).toBe("No explanations yet.");
  });
});

describe("learnText", () => {
  it("reads the proposals, then the learn thread", () => {
    const proposals = [
      proposal({ id: 3, title: "Test shortcut", payload: JSON.stringify({ name: "t", command: "make test" }), rationale: "ran it twice" }),
      proposal({ id: 4, kind: "rename", status: "failed", error: "taken", title: "Rename", payload: "not json" }),
      proposal({ id: 5, status: "failed", title: "Bare", payload: "x" }),
    ];
    const messages = [{ id: 1, author: "Loop", bot: true, content: "Reviewed." }];
    expect(learnText(proposals, messages, 50)).toBe(
      "Proposals:\n- #3 [pending] bash shortcut: Test shortcut\n  t → $ make test\n  why: ran it twice\n- #4 [failed (taken)] rename: Rename\n  not json\n- #5 [failed] bash shortcut: Bare\n  x\n\nLearn thread:\nLoop: Reviewed.",
    );
  });

  it("reads only the learn thread without proposals", () => {
    expect(learnText([], [{ id: 1, author: "Ann", bot: false, content: "hi" }], 50)).toBe("Learn thread:\nAnn: hi");
  });

  it("says when there's nothing yet", () => {
    expect(learnText([], [], 50)).toBe("No learn pass yet.");
  });
});
