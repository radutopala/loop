import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { unreadable } from "../test/hooks-c-api-fetch";
import { type AttentionItem, attentionActions, attentionKey, fetchAttention, groupAttention, needsFlash, nextSeen, runAttentionAction } from "./attention";
import { formatAskAnswers } from "./channels";

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.stubGlobal("window", { location: { hash: "" } });
  vi.stubGlobal("sessionStorage", { getItem: () => null, setItem: () => {} });
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const ask: AttentionItem = { kind: "ask", channel_id: "t1", channel_name: "one", parent_name: "proj", title: "Question", ref: "tu1", blocking: true };
const plan: AttentionItem = { kind: "plan", channel_id: "t2", title: "Plan to approve", ref: "tu2", blocking: true };
const pr: AttentionItem = { kind: "pr", channel_id: "t1", channel_name: "one", parent_name: "proj", title: "PR needs attention", blocking: false };
const run: AttentionItem = { kind: "run", channel_id: "t3", channel_name: "three", title: "Run failed", ref: "7", blocking: false };

describe("fetchAttention", () => {
  it.each([
    ["the items", { items: [ask] }, [ask]],
    ["none", { items: null }, []],
  ])("lists %s", async (_name, body, want) => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(body)));
    await expect(fetchAttention()).resolves.toEqual(want);
    expect(fetchMock.mock.calls[0]![0]).toBe("http://localhost:8222/api/attention");
  });

  it.each([
    ["the server's message", new Response("boom\n", { status: 500, statusText: "Internal Server Error" }), "Failed to list what needs you: boom"],
    ["the status without one", new Response("", { status: 403, statusText: "Forbidden" }), "Failed to list what needs you: Forbidden"],
  ])("throws with %s", async (_name, res, want) => {
    fetchMock.mockResolvedValue(res);
    await expect(fetchAttention()).rejects.toThrow(want);
  });

  it("throws with the status when the body can't be read", async () => {
    fetchMock.mockResolvedValue(unreadable(502, "Bad Gateway"));
    await expect(fetchAttention()).rejects.toThrow("Failed to list what needs you: Bad Gateway");
  });
});

describe("attentionKey", () => {
  it("joins kind, channel and ref, with an empty ref when there is none", () => {
    expect(attentionKey(run)).toBe("run\u0000t3\u00007");
    expect(attentionKey(pr)).toBe("pr\u0000t1\u0000");
  });
});

describe("groupAttention", () => {
  it("groups by channel in first-seen order, labelled by parent and name", () => {
    expect(groupAttention([ask, plan, pr, run])).toEqual([
      { channelId: "t1", label: "proj › one", items: [ask, pr] },
      { channelId: "t2", label: "t2", items: [plan] },
      { channelId: "t3", label: "three", items: [run] },
    ]);
  });
});

describe("needsFlash", () => {
  it.each([
    ["an unseen blocking item", [ask, pr], [], true],
    ["only seen blocking items", [ask, pr], [attentionKey(ask)], false],
    ["only non-blocking items", [pr, run], [], false],
  ])("%s", (_name, items, seen, want) => {
    expect(needsFlash(items, new Set(seen))).toBe(want);
  });
});

describe("nextSeen", () => {
  it.each([
    ["keeps the seen items still waiting", [ask, plan], [attentionKey(ask), attentionKey(run)], false, [attentionKey(ask)]],
    ["sees every item when shown", [ask, plan], [], true, [attentionKey(ask), attentionKey(plan)]],
  ])("%s", (_name, items, seen, shown, want) => {
    expect([...nextSeen(items, new Set(seen), shown)]).toEqual(want);
  });
});

const gate: AttentionItem = { kind: "gate", channel_id: "t4", title: "Run curl?", ref: "req 1", blocking: true };
const pick: AttentionItem = { ...ask, questions: [{ question: "Which?", options: [{ label: "A" }, { label: "B" }] }] };

describe("attentionActions", () => {
  const ids = (it: AttentionItem) => attentionActions(it).map((a) => a.id);
  it.each([
    ["a gate request", gate, ["once", "session", "deny"]],
    ["a plan", plan, ["approve", "reject"]],
    ["a one-pick question", pick, ["option:0", "option:1", "skip"]],
    ["a question without its questions", ask, []],
    ["a several-pick question", { ...pick, questions: [{ ...pick.questions![0]!, multiSelect: true }] }, []],
    ["several questions", { ...pick, questions: [pick.questions![0]!, pick.questions![0]!] }, []],
    ["a question without options", { ...pick, questions: [{ question: "Why?" }] }, []],
    ["a failed run", run, ["dismiss"]],
    ["a PR", pr, []],
  ])("%s", (_name, it, want) => {
    expect(ids(it)).toEqual(want);
  });
});

describe("runAttentionAction", () => {
  const sent = () => {
    const [url, init] = fetchMock.mock.calls[0]!;
    return { url, method: init?.method, body: init?.body ? JSON.parse(init.body as string) : undefined };
  };

  it.each([
    ["allows a gate request", gate, "session", { url: "http://localhost:8222/api/gate/approvals/req%201", method: "POST", body: { decision: "session" } }],
    ["approves a plan", plan, "approve", { url: "http://localhost:8222/api/channels/t2/plan/resolve", method: "POST", body: { action: "approve" } }],
    [
      "answers a question with the option picked",
      pick,
      "option:1",
      { url: "http://localhost:8222/api/channels/t1/ask/resolve", method: "POST", body: { action: "answer", answer: "Here are my answers:\n\nQ: Which?\nA: B" } },
    ],
    ["skips a question", pick, "skip", { url: "http://localhost:8222/api/channels/t1/ask/resolve", method: "POST", body: { action: "skip" } }],
    ["dismisses a failed run", run, "dismiss", { url: "http://localhost:8222/api/notifications/7", method: "DELETE", body: undefined }],
  ])("%s", async (_name, it, id, want) => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await runAttentionAction(it, id);
    expect(sent()).toEqual(want);
  });

  it("resolves a gate request without a ref under an empty id", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await runAttentionAction({ ...gate, ref: undefined }, "once");
    expect(sent().url).toBe("http://localhost:8222/api/gate/approvals/");
  });

  it("takes a gate request already gone as done", async () => {
    fetchMock.mockResolvedValue(new Response("gone", { status: 404 }));
    await expect(runAttentionAction(gate, "once")).resolves.toBeUndefined();
  });

  it("passes on a gate request's other failures", async () => {
    fetchMock.mockResolvedValue(new Response("boom", { status: 500, statusText: "Internal Server Error" }));
    await expect(runAttentionAction(gate, "deny")).rejects.toThrow();
  });

  it("does nothing for an item without actions", async () => {
    await runAttentionAction(pr, "x");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("formatAskAnswers", () => {
  it.each([
    ["no answers", [], "No specific answers provided."],
    [
      "each question with its picks",
      [
        { question: "Which?", labels: ["A"] },
        { question: "Who?", labels: ["B", "C"] },
      ],
      "Here are my answers:\n\nQ: Which?\nA: A\n\nQ: Who?\nA: B, C",
    ],
  ])("%s", (_name, answers, want) => {
    expect(formatAskAnswers(answers)).toBe(want);
  });
});
