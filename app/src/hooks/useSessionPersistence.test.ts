import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const KEY = "loop.terminalSessions";

function fakeStorage(initial?: string) {
  const data = new Map<string, string>();
  if (initial !== undefined) data.set(KEY, initial);
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

async function load(storage: unknown) {
  vi.resetModules();
  vi.stubGlobal("sessionStorage", storage);
  return import("./useSessionPersistence");
}

describe("stored terminal sessions", () => {
  beforeEach(() => vi.unstubAllGlobals());
  afterEach(() => vi.unstubAllGlobals());

  it("reads the sessions a reload left behind, and saves what it takes", async () => {
    const storage = fakeStorage(JSON.stringify({ ids: [["ch-1:agent:docker-shell-0", "sess-1"]], starts: [["ch-1:agent:docker-shell-0", 5]] }));
    const { heldSessionIds, takeStoredSession } = await load(storage);

    expect(heldSessionIds()).toEqual(["sess-1"]);
    expect(takeStoredSession("ch-1", "agent", "docker-shell-0")).toBe("sess-1");
    expect(heldSessionIds()).toEqual([]);
    expect(JSON.parse(storage.data.get(KEY)!)).toEqual({ ids: [], starts: [] });
    expect(takeStoredSession("ch-1", "agent", "docker-shell-0")).toBeNull();
  });

  it.each([
    ["no storage", undefined],
    ["a corrupt entry", fakeStorage("{not json")],
    ["an entry missing its maps", fakeStorage("{}")],
    [
      "storage that throws",
      {
        getItem: () => {
          throw new Error("blocked");
        },
        setItem: () => {
          throw new Error("blocked");
        },
      },
    ],
  ])("starts empty with %s", async (_name, storage) => {
    const { takeStoredSession } = await load(storage);
    expect(takeStoredSession("ch-1", "host")).toBeNull();
  });
});
