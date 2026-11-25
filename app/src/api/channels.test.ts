import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  closeTerminalSessionById,
  createChannel,
  createThread,
  createWorktreeThread,
  deleteChannel,
  deleteQueuedMessage,
  deleteThread,
  ensureChannel,
  fetchAgentConfig,
  fetchChannels,
  fetchComposerHistory,
  fetchContainerStats,
  fetchMessages,
  fetchPromptSuggestion,
  fetchQueuedMessages,
  fetchReadme,
  fetchTimeline,
  forkAtMessage,
  forkThread,
  formatAskAnswers,
  holdQueuedMessage,
  importWorktree,
  killAgentContainer,
  listPendingAsks,
  listPendingPlans,
  pasteImage,
  releaseQueuedHold,
  removeWorktree,
  renameChannel,
  reorderQueuedMessages,
  resolveAsk,
  resolvePlan,
  sendCommand,
  sendMessage,
  setChannelDescription,
  setChannelLocked,
  setChannelTicketURL,
  setWorktreeLocked,
  steerQueuedMessage,
  updateAgentConfig,
  updateQueuedMessage,
} from "./channels";

const API = "http://localhost:8222";

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

// respond answers every fetch with a fresh Response, so a body can be read on each call.
function respond(body: string | null, init: ResponseInit = {}) {
  fetchMock.mockImplementation(() => Promise.resolve(new Response(body, init)));
}

function json(value: unknown, init: ResponseInit = {}) {
  respond(JSON.stringify(value), init);
}

function sent(i = 0): { url: string; method?: string; body?: unknown } {
  const [url, init] = fetchMock.mock.calls[i]!;
  const raw = (init as RequestInit | undefined)?.body;
  return { url: String(url), method: (init as RequestInit | undefined)?.method, body: typeof raw === "string" ? JSON.parse(raw) : raw };
}

class FakeSocket {
  static instances: FakeSocket[] = [];
  sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(
    public url: string,
    public protocols: string[],
  ) {
    FakeSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
  }
}

describe("terminal socket commands", () => {
  beforeEach(() => {
    FakeSocket.instances = [];
    vi.stubGlobal("WebSocket", FakeSocket);
  });

  it.each([
    ["killAgentContainer", () => killAgentContainer("ch1"), { type: "kill", channel_id: "ch1" }, "message"],
    ["closeTerminalSessionById", () => closeTerminalSessionById("s1", "host"), { type: "close", session_id: "s1", target: "host" }, "error"],
  ] as const)("%s sends its command on open and closes on reply", (_name, run, want, closeOn) => {
    run();
    const ws = FakeSocket.instances[0]!;
    expect(ws.url).toBe("ws://localhost:8222/api/ws/terminal");
    expect(ws.protocols).toEqual(["loop"]);
    ws.onopen!();
    expect(ws.sent.map((s) => JSON.parse(s))).toEqual([want]);
    expect(ws.closed).toBe(false);
    if (closeOn === "message") ws.onmessage!();
    else ws.onerror!();
    expect(ws.closed).toBe(true);
  });

  it.each([
    ["killAgentContainer", () => killAgentContainer("ch1")],
    ["closeTerminalSessionById", () => closeTerminalSessionById("s1", "agent")],
  ] as const)("%s closes on both a reply and an error", (_name, run) => {
    run();
    run();
    const [a, b] = FakeSocket.instances;
    a!.onerror!();
    b!.onmessage!();
    expect(a!.closed).toBe(true);
    expect(b!.closed).toBe(true);
  });
});

describe("fetchChannels", () => {
  it("maps the daemon's channels and fills defaults", async () => {
    json([
      {
        channel_id: "c1",
        name: "one",
        dir_path: "/p",
        display_path: "~/p",
        parent_id: "",
        session_id: "s",
        active: true,
        container_running: true,
        agent_running: false,
        review_running: true,
        branch: "main",
        commit: "abc",
        worktree: true,
        base_branch: "dev",
        locked: true,
        diff_additions: 3,
        diff_deletions: 1,
        review_enabled: true,
        last_activity_at: "2026-01-02T03:04:05Z",
        task_id: 7,
      },
      { channel_id: "c2", name: "two", dir_path: "/q", parent_id: "c1", active: false, container_running: false, agent_running: false },
    ]);
    const [a, b] = await fetchChannels();
    expect(sent().url).toBe(`${API}/api/channels?platform=local`);
    expect(a).toMatchObject({
      id: "c1",
      session_id: "s",
      review_running: true,
      branch: "main",
      commit: "abc",
      worktree: true,
      base_branch: "dev",
      locked: true,
      diff_additions: 3,
      diff_deletions: 1,
      review_enabled: true,
      last_activity_at: Date.parse("2026-01-02T03:04:05Z"),
      task_id: 7,
    });
    expect(b).toMatchObject({
      id: "c2",
      parent_id: "c1",
      session_id: "",
      review_running: false,
      branch: "",
      commit: "",
      worktree: false,
      base_branch: "",
      locked: false,
      diff_additions: 0,
      diff_deletions: 0,
      review_enabled: false,
      last_activity_at: undefined,
    });
  });

  it("throws with the status text", async () => {
    respond("", { status: 500, statusText: "Internal Server Error" });
    await expect(fetchChannels()).rejects.toThrow("Failed to fetch channels: Internal Server Error");
  });
});

describe("ensureChannel", () => {
  it("POSTs the dir and maps the channel with defaults", async () => {
    json({ channel_id: "c1", name: "p", dir_path: "/p", parent_id: "", active: true, container_running: false, agent_running: false });
    await expect(ensureChannel("/p")).resolves.toEqual({
      id: "c1",
      name: "p",
      dir_path: "/p",
      parent_id: "",
      session_id: "",
      active: true,
      container_running: false,
      agent_running: false,
      branch: "",
      commit: "",
      worktree: false,
      base_branch: "",
      locked: false,
      diff_additions: 0,
      diff_deletions: 0,
      review_enabled: false,
    });
    expect(sent()).toEqual({ url: `${API}/api/channels`, method: "POST", body: { dir_path: "/p", platform: "local" } });
  });

  it("keeps the values the daemon sends", async () => {
    json({
      channel_id: "c1",
      name: "p",
      dir_path: "/p",
      parent_id: "",
      session_id: "s",
      active: true,
      container_running: true,
      agent_running: true,
      branch: "b",
      commit: "c",
      worktree: true,
      base_branch: "main",
      locked: true,
      diff_additions: 2,
      diff_deletions: 4,
      review_enabled: true,
    });
    await expect(ensureChannel("/p")).resolves.toMatchObject({
      session_id: "s",
      branch: "b",
      commit: "c",
      worktree: true,
      base_branch: "main",
      locked: true,
      diff_additions: 2,
      diff_deletions: 4,
      review_enabled: true,
    });
  });

  it("throws the daemon's message", async () => {
    respond("not a directory", { status: 400 });
    await expect(ensureChannel("/x")).rejects.toThrow("not a directory");
  });
});

describe("writes", () => {
  const cases: {
    name: string;
    run: () => Promise<unknown>;
    url: string;
    method: string;
    body?: unknown;
    reply?: unknown;
    want?: unknown;
    err: string;
  }[] = [
    {
      name: "createThread with a session",
      run: () => createThread("ch1", "t", "s1"),
      url: `${API}/api/threads`,
      method: "POST",
      body: { channel_id: "ch1", name: "t", author_id: "desktop", session_id: "s1" },
      reply: { thread_id: "th1" },
      want: "th1",
      err: "Failed to create thread: Bad",
    },
    {
      name: "createThread without a session",
      run: () => createThread("ch1", "t"),
      url: `${API}/api/threads`,
      method: "POST",
      body: { channel_id: "ch1", name: "t", author_id: "desktop" },
      reply: { thread_id: "th1" },
      want: "th1",
      err: "Failed to create thread: Bad",
    },
    { name: "deleteThread", run: () => deleteThread("th1"), url: `${API}/api/threads/th1`, method: "DELETE", err: "Failed to delete thread: Bad" },
    {
      name: "removeWorktree with a thread",
      run: () => removeWorktree("ch1", "/wt", "th1"),
      url: `${API}/api/worktrees`,
      method: "DELETE",
      body: { channel_id: "ch1", worktree_path: "/wt", thread_id: "th1" },
      err: "Failed to remove worktree: Bad",
    },
    {
      name: "removeWorktree without a thread",
      run: () => removeWorktree("ch1", "/wt"),
      url: `${API}/api/worktrees`,
      method: "DELETE",
      body: { channel_id: "ch1", worktree_path: "/wt" },
      err: "Failed to remove worktree: Bad",
    },
    {
      name: "setWorktreeLocked",
      run: () => setWorktreeLocked("ch1", "/wt", true),
      url: `${API}/api/worktrees/lock`,
      method: "POST",
      body: { channel_id: "ch1", worktree_path: "/wt", locked: true },
      err: "Failed to update worktree lock: Bad",
    },
    { name: "deleteChannel", run: () => deleteChannel("ch1"), url: `${API}/api/channels/ch1`, method: "DELETE", err: "Failed to delete channel: Bad" },
    { name: "setChannelLocked", run: () => setChannelLocked("ch1", false), url: `${API}/api/channels/ch1/lock`, method: "PATCH", body: { locked: false }, err: "Failed to update lock: Bad" },
    {
      name: "resolvePlan with prompt and mode",
      run: () => resolvePlan("ch1", "reject", "redo", "plan"),
      url: `${API}/api/channels/ch1/plan/resolve`,
      method: "POST",
      body: { action: "reject", prompt: "redo", mode: "plan" },
      err: "Failed to resolve plan: Bad",
    },
    {
      name: "resolvePlan bare",
      run: () => resolvePlan("ch1", "approve"),
      url: `${API}/api/channels/ch1/plan/resolve`,
      method: "POST",
      body: { action: "approve" },
      err: "Failed to resolve plan: Bad",
    },
    {
      name: "resolveAsk with answer and mode",
      run: () => resolveAsk("ch1", "answer", "yes", "agent"),
      url: `${API}/api/channels/ch1/ask/resolve`,
      method: "POST",
      body: { action: "answer", answer: "yes", mode: "agent" },
      err: "Failed to resolve ask: Bad",
    },
    { name: "resolveAsk bare", run: () => resolveAsk("ch1", "skip"), url: `${API}/api/channels/ch1/ask/resolve`, method: "POST", body: { action: "skip" }, err: "Failed to resolve ask: Bad" },
    {
      name: "createChannel",
      run: () => createChannel("new"),
      url: `${API}/api/channels/create`,
      method: "POST",
      body: { name: "new", platform: "local" },
      reply: { channel_id: "c9" },
      want: "c9",
      err: "Failed to create channel: Bad",
    },
    {
      name: "sendMessage in agent mode with queue delivery",
      run: () => sendMessage("ch1", "hi", "agent", "queue"),
      url: `${API}/api/messages`,
      method: "POST",
      body: { channel_id: "ch1", content: "hi" },
      err: "Failed to send message: Bad",
    },
    {
      name: "sendMessage in plan mode with steer delivery",
      run: () => sendMessage("ch1", "hi", "plan", "steer"),
      url: `${API}/api/messages`,
      method: "POST",
      body: { channel_id: "ch1", content: "hi", mode: "plan", delivery: "steer" },
      err: "Failed to send message: Bad",
    },
    {
      name: "pasteImage",
      run: () => pasteImage("c/1", "AAA", "image/png"),
      url: `${API}/api/channels/c%2F1/paste-image`,
      method: "POST",
      body: { data: "AAA", media_type: "image/png" },
      reply: { path: "/tmp/x.png" },
      want: "/tmp/x.png",
      err: "Failed to paste image: Bad",
    },
    { name: "deleteQueuedMessage", run: () => deleteQueuedMessage("c 1", "m/1"), url: `${API}/api/messages/m%2F1?channel_id=c%201`, method: "DELETE", err: "Failed to delete queued message: Bad" },
    { name: "steerQueuedMessage", run: () => steerQueuedMessage("ch1", "m1"), url: `${API}/api/channels/ch1/queued/m1/steer`, method: "POST", err: "Failed to steer queued message: Bad" },
    { name: "holdQueuedMessage", run: () => holdQueuedMessage("ch1", "m1"), url: `${API}/api/channels/ch1/queued/m1/hold`, method: "POST", want: true, err: "Failed to hold queued message: Bad" },
    { name: "releaseQueuedHold", run: () => releaseQueuedHold("ch1", "m1"), url: `${API}/api/channels/ch1/queued/m1/hold`, method: "DELETE", err: "Failed to release queued message: Bad" },
    {
      name: "updateQueuedMessage",
      run: () => updateQueuedMessage("ch1", "m1", "new"),
      url: `${API}/api/channels/ch1/queued/m1`,
      method: "PUT",
      body: { content: "new" },
      want: true,
      err: "Failed to update queued message: Bad",
    },
    {
      name: "reorderQueuedMessages",
      run: () => reorderQueuedMessages("ch1", ["b", "a"]),
      url: `${API}/api/channels/ch1/queued/reorder`,
      method: "POST",
      body: { order: ["b", "a"] },
      err: "Failed to reorder queued messages: Bad",
    },
    { name: "sendCommand", run: () => sendCommand("ch1", "/clear"), url: `${API}/api/commands`, method: "POST", body: { channel_id: "ch1", command: "/clear" }, err: "Failed to send command: Bad" },
    {
      name: "importWorktree",
      run: () => importWorktree("ch1", "/wt"),
      url: `${API}/api/worktrees/import`,
      method: "POST",
      body: { channel_id: "ch1", worktree_path: "/wt" },
      reply: { thread_id: "th1", worktree_path: "/wt" },
      want: { threadId: "th1", worktreePath: "/wt" },
      err: "Failed to import worktree: Bad",
    },
    {
      name: "updateAgentConfig",
      run: () => updateAgentConfig("ch1", "opus", "high"),
      url: `${API}/api/channels/ch1/agent-config`,
      method: "PATCH",
      body: { model: "opus", effort: "high" },
      err: "Failed to update agent config: Bad",
    },
  ];

  it.each(cases)("$name sends the request", async (c) => {
    if (c.reply !== undefined) json(c.reply);
    else respond(null, { status: 204 });
    const got = await c.run();
    if (c.want !== undefined) expect(got).toEqual(c.want);
    const s = sent();
    expect(s.url).toBe(c.url);
    expect(s.method).toBe(c.method);
    expect(s.body).toEqual(c.body);
  });

  it.each(cases)("$name throws on failure", async (c) => {
    respond("", { status: 500, statusText: "Bad" });
    await expect(c.run()).rejects.toThrow(c.err);
  });
});

describe("queued message conflicts", () => {
  it("holdQueuedMessage reports a message already sent", async () => {
    respond("", { status: 409 });
    await expect(holdQueuedMessage("ch1", "m1")).resolves.toBe(false);
  });

  it("updateQueuedMessage reports a message already sent", async () => {
    respond("", { status: 409 });
    await expect(updateQueuedMessage("ch1", "m1", "x")).resolves.toBe(false);
  });
});

describe("daemon messages in errors", () => {
  it.each([
    ["renameChannel", () => renameChannel("ch1", "n"), `${API}/api/channels/ch1/rename`, { name: "n" }, "Failed to rename thread"],
    ["setChannelDescription", () => setChannelDescription("ch1", "d"), `${API}/api/channels/ch1/description`, { description: "d" }, "Failed to set description"],
    ["setChannelTicketURL", () => setChannelTicketURL("ch1", "u"), `${API}/api/channels/ch1/ticket`, { ticket_url: "u" }, "Failed to set ticket"],
  ] as const)("%s POSTs and reports the body or status text", async (_name, run, url, body, prefix) => {
    respond(null, { status: 204 });
    await run();
    expect(sent()).toEqual({ url, method: "POST", body });

    respond("  name taken \n", { status: 409, statusText: "Conflict" });
    await expect(run()).rejects.toThrow(`${prefix}: name taken`);

    respond("", { status: 500, statusText: "Internal Server Error" });
    await expect(run()).rejects.toThrow(`${prefix}: Internal Server Error`);
  });

  it("falls back to the status text when the body can't be read", async () => {
    const res = new Response("", { status: 500, statusText: "Broken" });
    vi.spyOn(res, "text").mockRejectedValue(new Error("stream"));
    vi.spyOn(res, "clone").mockReturnValue(res);
    fetchMock.mockResolvedValue(res);
    await expect(renameChannel("ch1", "n")).rejects.toThrow("Failed to rename thread: Broken");
  });

  it("updateAgentConfig prefers the daemon's message", async () => {
    respond(" unknown model ", { status: 400, statusText: "Bad Request" });
    await expect(updateAgentConfig("ch1", "x", "y")).rejects.toThrow(/^unknown model$/);
  });

  it("updateAgentConfig falls back when the body can't be read", async () => {
    const res = new Response("", { status: 500, statusText: "Broken" });
    vi.spyOn(res, "text").mockRejectedValue(new Error("stream"));
    fetchMock.mockResolvedValue(res);
    await expect(updateAgentConfig("ch1", "x", "y")).rejects.toThrow("Failed to update agent config: Broken");
  });

  it("createWorktreeThread sends name and PR and reports the body or status text", async () => {
    json({ thread_id: "th1", worktree_path: "/wt" });
    await expect(createWorktreeThread("ch1", "feat", "Feature", 12)).resolves.toEqual({ threadId: "th1", worktreePath: "/wt" });
    expect(sent()).toEqual({ url: `${API}/api/worktrees`, method: "POST", body: { channel_id: "ch1", branch: "feat", name: "Feature", pr: 12 } });

    respond(" branch exists\n", { status: 409 });
    await expect(createWorktreeThread("ch1", "feat")).rejects.toThrow("Failed to create worktree: branch exists");
    expect(sent(1).body).toEqual({ channel_id: "ch1", branch: "feat" });

    respond("", { status: 500, statusText: "Bad" });
    await expect(createWorktreeThread("ch1", "feat")).rejects.toThrow("Failed to create worktree: Bad");
  });

  it("forkThread returns the new thread or reports the body or status text", async () => {
    json({ thread_id: "th2" });
    await expect(forkThread("a/b")).resolves.toBe("th2");
    expect(sent()).toMatchObject({ url: `${API}/api/threads/a%2Fb/fork`, method: "POST" });

    respond("no session", { status: 400 });
    await expect(forkThread("a")).rejects.toThrow("Failed to fork thread: no session");

    respond("", { status: 500, statusText: "Bad" });
    await expect(forkThread("a")).rejects.toThrow("Failed to fork thread: Bad");
  });

  it("forkAtMessage returns the fork or reports the body or status text", async () => {
    json({ thread_id: "th3" });
    await expect(forkAtMessage("c 1", "m/1")).resolves.toEqual({ thread_id: "th3" });
    expect(sent()).toMatchObject({ url: `${API}/api/channels/c%201/messages/m%2F1/fork`, method: "POST" });

    respond("no message", { status: 404 });
    await expect(forkAtMessage("c", "m")).rejects.toThrow(/^no message$/);

    respond("", { status: 500, statusText: "Bad" });
    await expect(forkAtMessage("c", "m")).rejects.toThrow(/^Bad$/);
  });
});

describe("formatAskAnswers", () => {
  it("says no answers were given for an empty list", () => {
    expect(formatAskAnswers([])).toBe("No specific answers provided.");
  });

  it("lists each question with its chosen labels", () => {
    expect(
      formatAskAnswers([
        { question: "Color?", labels: ["red", "blue"] },
        { question: "Size?", labels: ["L"] },
      ]),
    ).toBe("Here are my answers:\n\nQ: Color?\nA: red, blue\n\nQ: Size?\nA: L");
  });
});

describe("pending asks and plans", () => {
  it.each([
    ["listPendingAsks", listPendingAsks, `${API}/api/asks/pending`, "asks", "Failed to list pending asks: Bad"],
    ["listPendingPlans", listPendingPlans, `${API}/api/plans/pending`, "plans", "Failed to list pending plans: Bad"],
  ] as const)("%s returns the list, an empty one for a missing key, or throws", async (_name, run, url, key, err) => {
    json({ [key]: [{ channel_id: "c1" }] });
    await expect(run()).resolves.toEqual([{ channel_id: "c1" }]);
    expect(sent().url).toBe(url);

    json({});
    await expect(run()).resolves.toEqual([]);

    respond("", { status: 500, statusText: "Bad" });
    await expect(run()).rejects.toThrow(err);
  });
});

describe("reads", () => {
  it.each([
    [{}, `${API}/api/channels/ch1/messages?`],
    [{ limit: 20, cursor: 5 }, `${API}/api/channels/ch1/messages?limit=20&cursor=5`],
    [{ around: 9, cursor: 5 }, `${API}/api/channels/ch1/messages?around=9`],
  ])("fetchMessages GETs %o", async (opts, url) => {
    json({ messages: [], has_more: false });
    await expect(fetchMessages("ch1", opts)).resolves.toEqual({ messages: [], has_more: false });
    expect(sent().url).toBe(url);
  });

  it("fetchMessages works without options", async () => {
    json({ messages: [] });
    await fetchMessages("ch1");
    expect(sent().url).toBe(`${API}/api/channels/ch1/messages?`);
  });

  it.each([
    [undefined, `${API}/api/channels/ch1/timeline?`],
    [{ limit: 10, cursorPosition: 0, cursorId: 0 }, `${API}/api/channels/ch1/timeline?limit=10&cursor_position=0&cursor_id=0`],
  ])("fetchTimeline GETs %o", async (opts, url) => {
    json({ items: [] });
    await expect(fetchTimeline("ch1", opts)).resolves.toEqual({ items: [] });
    expect(sent().url).toBe(url);
  });

  it("fetchQueuedMessages returns the queue, or an empty one", async () => {
    json({ messages: [{ id: 1 }] });
    await expect(fetchQueuedMessages("ch1")).resolves.toEqual([{ id: 1 }]);
    expect(sent().url).toBe(`${API}/api/channels/ch1/queued`);
    json({});
    await expect(fetchQueuedMessages("ch1")).resolves.toEqual([]);
  });

  it("fetchReadme returns the text", async () => {
    respond("# Loop");
    await expect(fetchReadme()).resolves.toBe("# Loop");
    expect(sent().url).toBe(`${API}/api/readme`);
  });

  it("fetchComposerHistory uses the default limit and handles a missing list", async () => {
    json({ messages: ["a", "b"] });
    await expect(fetchComposerHistory("ch1")).resolves.toEqual(["a", "b"]);
    expect(sent().url).toBe(`${API}/api/channels/ch1/composer-history?limit=100`);
    json({});
    await expect(fetchComposerHistory("ch1", 5)).resolves.toEqual([]);
    expect(sent(1).url).toBe(`${API}/api/channels/ch1/composer-history?limit=5`);
  });

  it("fetchPromptSuggestion returns the suggestion, or an empty one", async () => {
    json({ suggestion: "run the tests" });
    await expect(fetchPromptSuggestion("ch1")).resolves.toBe("run the tests");
    expect(sent().url).toBe(`${API}/api/channels/ch1/prompt-suggestion`);
    json({});
    await expect(fetchPromptSuggestion("ch1")).resolves.toBe("");
  });

  it("fetchAgentConfig returns the config", async () => {
    json({ model: "opus", effort: "high" });
    await expect(fetchAgentConfig("ch1")).resolves.toEqual({ model: "opus", effort: "high" });
    expect(sent().url).toBe(`${API}/api/channels/ch1/agent-config`);
  });

  it("fetchContainerStats returns the entries", async () => {
    json([{ type: "agent" }]);
    await expect(fetchContainerStats("c/1")).resolves.toEqual([{ type: "agent" }]);
    expect(sent().url).toBe(`${API}/api/channels/c%2F1/container-stats`);
  });

  it.each([
    ["fetchMessages", () => fetchMessages("ch1"), "Failed to fetch messages: Bad"],
    ["fetchQueuedMessages", () => fetchQueuedMessages("ch1"), "Failed to fetch queued messages: Bad"],
    ["fetchTimeline", () => fetchTimeline("ch1"), "Failed to fetch timeline: Bad"],
    ["fetchReadme", () => fetchReadme(), "Failed to fetch README: Bad"],
    ["fetchComposerHistory", () => fetchComposerHistory("ch1"), "Failed to fetch composer history: Bad"],
    ["fetchPromptSuggestion", () => fetchPromptSuggestion("ch1"), "Failed to fetch the prompt suggestion: Bad"],
    ["fetchAgentConfig", () => fetchAgentConfig("ch1"), "Failed to fetch agent config: Bad"],
    ["fetchContainerStats", () => fetchContainerStats("ch1"), "Failed to fetch container stats: Bad"],
  ] as const)("%s throws with the status text", async (_name, run, err) => {
    respond("", { status: 500, statusText: "Bad" });
    await expect(run()).rejects.toThrow(err);
  });
});
