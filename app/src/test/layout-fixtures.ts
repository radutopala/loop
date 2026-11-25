import { vi } from "vitest";
import type { Explanation } from "../api/explain";
import type { LearnProposal } from "../api/learn";
import type { ExplainView } from "../hooks/useExplain";
import type { LearnView } from "../hooks/useLearn";
import type { Channel } from "../types";

/** A channel with every required field set, overridden by over. */
export function layoutChannel(over: Partial<Channel> = {}): Channel {
  return {
    id: "ch-1",
    name: "general",
    parent_id: "",
    dir_path: "/home/u/proj",
    session_id: "",
    active: true,
    container_running: false,
    agent_running: false,
    branch: "",
    commit: "",
    worktree: false,
    locked: false,
    diff_additions: 0,
    diff_deletions: 0,
    review_enabled: false,
    ...over,
  };
}

/** A finished explanation of turn m-1 in ch-1, overridden by over. */
export function layoutExplanation(over: Partial<Explanation> = {}): Explanation {
  return {
    id: 1,
    channel_id: "ch-1",
    message_id: "m-1",
    explain_channel_id: "ex-1",
    status: "done",
    content: "",
    created_at: "2026-01-01T10:00:00Z",
    updated_at: "2026-01-01T10:00:00Z",
    ...over,
  };
}

/** A loaded ExplainView with nothing explained, its actions mocks. */
export function layoutExplainView(over: Partial<ExplainView> = {}): ExplainView {
  return {
    loaded: true,
    available: true,
    explain: "",
    defaultExplain: false,
    setExplain: vi.fn(),
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

/** A pending description proposal, overridden by over. */
export function layoutProposal(over: Partial<LearnProposal> = {}): LearnProposal {
  return {
    id: 1,
    channel_id: "ch-1",
    learn_channel_id: "lc-1",
    kind: "description",
    title: "Describe it",
    rationale: "",
    payload: '{"description":"a thread"}',
    status: "pending",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

/** A loaded LearnView, its actions mocks; open defaults to the pending and
 *  failed proposals. */
export function layoutLearnView(over: Partial<LearnView> = {}): LearnView {
  const proposals = over.proposals ?? [];
  return {
    loaded: true,
    available: true,
    learn: "",
    defaultLearn: false,
    setLearn: vi.fn(),
    learnChannelId: "",
    running: false,
    proposals,
    open: proposals.filter((p) => p.status === "pending" || p.status === "failed"),
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
