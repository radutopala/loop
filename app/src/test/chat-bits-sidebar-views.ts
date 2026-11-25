import { vi } from "vitest";
import type { Explanation } from "../api/explain";
import type { LearnPass, LearnProposal } from "../api/learn";
import type { ExplainView } from "../hooks/useExplain";
import type { LearnView } from "../hooks/useLearn";

// Builders for the views the chat's Learn and Explain controls read from
// context, with every callback a mock.

export function explainView(over: Partial<ExplainView> = {}): ExplainView {
  return {
    loaded: true,
    available: true,
    explain: "",
    defaultExplain: false,
    setExplain: vi.fn().mockResolvedValue(undefined),
    explanations: [],
    byMessage: new Map(),
    busy: new Set(),
    errors: new Map(),
    focus: null,
    explainTurn: vi.fn().mockResolvedValue(undefined),
    show: vi.fn(),
    ...over,
  };
}

export function learnView(over: Partial<LearnView> = {}): LearnView {
  return {
    loaded: true,
    available: true,
    learn: "",
    defaultLearn: false,
    setLearn: vi.fn().mockResolvedValue(undefined),
    learnChannelId: "",
    running: false,
    proposals: [],
    open: [],
    busy: new Set(),
    errors: new Map(),
    bulk: null,
    apply: vi.fn().mockResolvedValue(undefined),
    dismiss: vi.fn().mockResolvedValue(undefined),
    applyAll: vi.fn().mockResolvedValue(undefined),
    dismissAll: vi.fn().mockResolvedValue(undefined),
    shortcutsVersion: 0,
    passes: [],
    passByMessage: new Map(),
    focus: null,
    show: vi.fn(),
    learnTurn: vi.fn().mockResolvedValue(undefined),
    turnBusy: new Set(),
    turnErrors: new Map(),
    ...over,
  };
}

export function explanation(over: Partial<Explanation> = {}): Explanation {
  return { id: 1, channel_id: "ch-1", message_id: "m-1", explain_channel_id: "ex-1", status: "done", content: "", created_at: "", updated_at: "", ...over };
}

export function learnPass(over: Partial<LearnPass> = {}): LearnPass {
  return { id: 1, channel_id: "ch-1", message_id: "m-1", learn_channel_id: "lc-1", status: "done", created_at: "", updated_at: "", ...over };
}

export function learnProposal(over: Partial<LearnProposal> = {}): LearnProposal {
  return {
    id: 1,
    channel_id: "ch-1",
    learn_channel_id: "lc-1",
    kind: "memory",
    title: "t",
    rationale: "r",
    payload: "{}",
    status: "pending",
    message_id: "m-1",
    created_at: "",
    updated_at: new Date().toISOString(),
    ...over,
  };
}

/** Mounts a pane header slot for leafId, as the pane header renders it. */
export function mountPaneHeaderSlot(leafId: string): HTMLElement {
  const el = document.createElement("div");
  el.id = `pane-header-slot-${leafId}`;
  document.body.appendChild(el);
  return el;
}
