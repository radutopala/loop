import { describe, expect, it, vi } from "vitest";

// The handler is a plain function under useCallback; run it outside React.
vi.mock("react", () => ({ useCallback: <T>(fn: T) => fn }));

const { useTerminalMessageDispatcher } = await import("./useTerminalMessageDispatcher");

function setup() {
  const opts = { onData: vi.fn(), onStatus: vi.fn(), onError: vi.fn(), onSessionChange: vi.fn(), onSessionFailed: vi.fn(), onCreated: vi.fn() };
  const { handleMessage } = useTerminalMessageDispatcher(opts);
  const send = (msg: object) => handleMessage({ data: JSON.stringify(msg) } as MessageEvent);
  return { opts, send };
}

describe("terminal error messages", () => {
  it("replaces a session the server no longer has without showing an error", () => {
    const { opts, send } = setup();
    send({ type: "error", message: "session not found", error_code: "session_gone" });
    expect(opts.onError).not.toHaveBeenCalled();
    expect(opts.onSessionChange).toHaveBeenCalledWith(null);
    expect(opts.onSessionFailed).toHaveBeenCalled();
  });

  it.each(["session_failed", "no_session"])("shows a %s error and retries", (code) => {
    const { opts, send } = setup();
    send({ type: "error", message: "boom", error_code: code });
    expect(opts.onError).toHaveBeenCalledWith("boom");
    expect(opts.onSessionFailed).toHaveBeenCalled();
  });

  it("shows other errors without retrying", () => {
    const { opts, send } = setup();
    send({ type: "error", message: "bad input", error_code: "invalid_input" });
    expect(opts.onError).toHaveBeenCalledWith("bad input");
    expect(opts.onSessionFailed).not.toHaveBeenCalled();
  });
});

describe("terminal session messages", () => {
  it("tells the pane a new session was created, before the session changes", () => {
    const { opts, send } = setup();
    send({ type: "created", session_id: "sess-1" });
    expect(opts.onCreated).toHaveBeenCalledOnce();
    expect(opts.onCreated.mock.invocationCallOrder[0]).toBeLessThan(opts.onSessionChange.mock.invocationCallOrder[0]!);
    expect(opts.onSessionChange).toHaveBeenCalledWith("sess-1");
    expect(opts.onStatus).toHaveBeenCalledWith("running");
  });

  it("doesn't on an attach", () => {
    const { opts, send } = setup();
    send({ type: "attached", session_id: "sess-1" });
    expect(opts.onCreated).not.toHaveBeenCalled();
    expect(opts.onSessionChange).toHaveBeenCalledWith("sess-1");
    expect(opts.onStatus).toHaveBeenCalledWith("running");
  });
});

describe("terminal data and endings", () => {
  it("passes binary output straight through", () => {
    const { opts } = setup();
    const { handleMessage } = useTerminalMessageDispatcher(opts);
    const buf = new ArrayBuffer(4);
    handleMessage({ data: buf } as MessageEvent);
    expect(opts.onData).toHaveBeenCalledWith(buf);
    expect(opts.onStatus).not.toHaveBeenCalled();
  });

  it.each(["stopped", "closed"])("ends the session on %s", (type) => {
    const { opts, send } = setup();
    send({ type });
    expect(opts.onSessionChange).toHaveBeenCalledWith(null);
    expect(opts.onStatus).toHaveBeenCalledWith("completed");
  });

  it.each(["created", "attached"])("reports no session when %s carries no id", (type) => {
    const { opts, send } = setup();
    send({ type });
    expect(opts.onSessionChange).toHaveBeenCalledWith(null);
  });

  it("ignores message types it doesn't know", () => {
    const { opts, send } = setup();
    send({ type: "resized" });
    for (const fn of Object.values(opts)) expect(fn).not.toHaveBeenCalled();
  });

  it("works without the optional callbacks", () => {
    const opts = { onData: vi.fn(), onStatus: vi.fn(), onError: vi.fn(), onSessionChange: vi.fn() };
    const { handleMessage } = useTerminalMessageDispatcher(opts);
    handleMessage({ data: JSON.stringify({ type: "created", session_id: "s" }) } as MessageEvent);
    handleMessage({ data: JSON.stringify({ type: "error", message: "x", error_code: "no_session" }) } as MessageEvent);
    expect(opts.onSessionChange).toHaveBeenCalledWith("s");
    expect(opts.onSessionChange).toHaveBeenLastCalledWith(null);
    expect(opts.onError).toHaveBeenCalledWith("x");
  });
});
