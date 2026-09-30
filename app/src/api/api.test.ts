import { afterEach, describe, expect, it, vi } from "vitest";

type ApiModule = typeof import("./api");

// A fresh module per test: the token and the content-link cache are module
// state.
async function load(): Promise<ApiModule> {
  vi.resetModules();
  return import("./api");
}

function stubBrowser(hash = "", stored: Record<string, string> = {}) {
  const store = new Map(Object.entries(stored));
  const replaceState = vi.fn();
  vi.stubGlobal("window", { location: { hash, pathname: "/app", search: "?x=1", origin: "http://ui" } });
  vi.stubGlobal("history", { replaceState });
  vi.stubGlobal("sessionStorage", {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => store.set(k, v),
  });
  return { store, replaceState };
}

function stubDesktop(tokens: string[]) {
  const getApiToken = vi.fn(async () => tokens.shift() ?? "");
  vi.stubGlobal("window", { location: { hash: "" }, loopAPI: { getApiUrl: async () => "http://api", getApiToken } });
  return getApiToken;
}

function authOf(init: RequestInit | undefined): string | null {
  return new Headers(init?.headers).get("Authorization");
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("bootstrapTokenFromHash", () => {
  it("moves the token into sessionStorage and drops it from the URL", async () => {
    const { store, replaceState } = stubBrowser("#loop_token=abc%2B1");
    const api = await load();
    api.bootstrapTokenFromHash();
    expect(store.get("loop-api-token")).toBe("abc+1");
    expect(replaceState).toHaveBeenCalledWith(null, "", "/app?x=1");
    await expect(api.refreshApiToken()).resolves.toBe("abc+1");
  });

  it("leaves other fragments alone", async () => {
    const { store, replaceState } = stubBrowser("#channel-1/5");
    const api = await load();
    api.bootstrapTokenFromHash();
    expect(store.size).toBe(0);
    expect(replaceState).not.toHaveBeenCalled();
  });

  it("drops an empty token without storing it", async () => {
    const { store, replaceState } = stubBrowser("#loop_token=");
    const api = await load();
    expect(api.bootstrapTokenFromHash()).toBe(false);
    expect(store.size).toBe(0);
    expect(replaceState).toHaveBeenCalled();
  });
});

describe("watchTokenHash", () => {
  function stubOpenTab(hash: string) {
    const { store } = stubBrowser(hash);
    const w = window as unknown as { location: { hash: string; reload: () => void }; addEventListener: (t: string, fn: () => void) => void };
    const listeners: Array<() => void> = [];
    w.location.reload = vi.fn();
    w.addEventListener = (t: string, fn: () => void) => {
      if (t === "hashchange") listeners.push(fn);
    };
    return { store, location: w.location, fire: () => listeners.forEach((fn) => fn()) };
  }

  it("takes a token pasted into an open tab and reloads", async () => {
    const tab = stubOpenTab("");
    const api = await load();
    api.watchTokenHash();
    tab.location.hash = "#loop_token=abc";
    tab.fire();
    expect(tab.store.get("loop-api-token")).toBe("abc");
    expect(tab.location.reload).toHaveBeenCalledOnce();

    // The same token again isn't news: no reload, so nothing can loop.
    tab.location.hash = "#loop_token=abc";
    tab.fire();
    expect(tab.location.reload).toHaveBeenCalledOnce();
  });

  it("ignores other fragment changes", async () => {
    const tab = stubOpenTab("");
    const api = await load();
    api.watchTokenHash();
    tab.location.hash = "#channel-1";
    tab.fire();
    expect(tab.location.reload).not.toHaveBeenCalled();
  });
});

describe("refreshApiToken", () => {
  it("asks the desktop app each time", async () => {
    stubDesktop(["one", "two"]);
    const api = await load();
    await expect(api.refreshApiToken()).resolves.toBe("one");
    await expect(api.refreshApiToken()).resolves.toBe("two");
    expect(api.getApiToken()).toBe("two");
  });

  it("is empty in a browser tab without a token", async () => {
    stubBrowser();
    const api = await load();
    await expect(api.refreshApiToken()).resolves.toBe("");
  });
});

describe("apiFetch", () => {
  it("sends the token and keeps the caller's headers", async () => {
    stubBrowser("", { "loop-api-token": "tok" });
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response("ok"));
    vi.stubGlobal("fetch", fetchMock);
    const api = await load();
    const res = await api.apiFetch("http://api/x", { method: "POST", headers: { "Content-Type": "application/json" } });
    expect(res.status).toBe(200);
    const init = fetchMock.mock.calls[0]![1];
    expect(authOf(init)).toBe("Bearer tok");
    expect(new Headers(init?.headers).get("Content-Type")).toBe("application/json");
    expect(init?.method).toBe("POST");
  });

  it("sends no header without a token", async () => {
    stubBrowser();
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response("", { status: 401 }));
    vi.stubGlobal("fetch", fetchMock);
    const api = await load();
    const res = await api.apiFetch("http://api/x");
    expect(res.status).toBe(401);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(authOf(fetchMock.mock.calls[0]![1])).toBeNull();
  });

  it("reads a rotated token on a 401 and retries once", async () => {
    stubDesktop(["old", "new"]);
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => new Response("", { status: authOf(init) === "Bearer new" ? 200 : 401 }));
    vi.stubGlobal("fetch", fetchMock);
    const api = await load();
    const res = await api.apiFetch("http://api/x");
    expect(res.status).toBe(200);
    expect(fetchMock.mock.calls.map((c) => authOf(c[1]))).toEqual(["Bearer old", "Bearer new"]);
  });

  it("doesn't retry when the token is unchanged", async () => {
    stubDesktop(["same", "same"]);
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response("", { status: 401 }));
    vi.stubGlobal("fetch", fetchMock);
    const api = await load();
    expect((await api.apiFetch("http://api/x")).status).toBe(401);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("wsProtocols", () => {
  it("carries the token as a subprotocol", async () => {
    stubBrowser("", { "loop-api-token": "tok" });
    const api = await load();
    expect(api.wsProtocols()).toEqual(["loop"]);
    await api.refreshApiToken();
    expect(api.wsProtocols()).toEqual(["loop", "loop.token.tok"]);
  });
});

describe("contentCapBase", () => {
  async function setup(status = 200) {
    stubDesktop(["tok", "tok", "tok"]);
    let n = 0;
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => {
      n++;
      return new Response(JSON.stringify({ base_url: `/c/cap${n}/`, expires_in_sec: 100 }), { status });
    });
    vi.stubGlobal("fetch", fetchMock);
    const api = await load();
    await api.initApiUrl();
    return { api, fetchMock };
  }

  it("mints a link per scope and caches it", async () => {
    const { api, fetchMock } = await setup();
    const raw = { kind: "raw", channelId: "ch", root: 1 } as const;
    await expect(api.contentCapBase(raw)).resolves.toBe("http://api/c/cap1/");
    await expect(api.contentCapBase(raw)).resolves.toBe("http://api/c/cap1/");
    await expect(api.contentCapBase({ kind: "playground", name: "demo" })).resolves.toBe("http://api/c/cap2/");
    await expect(api.contentCapBase({ kind: "playground", channelId: "ch", name: "demo" })).resolves.toBe("http://api/c/cap3/");
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const bodies = fetchMock.mock.calls.map((c) => JSON.parse(String(c[1]?.body)));
    expect(bodies).toEqual([
      { kind: "raw", channel_id: "ch", root: 1 },
      { kind: "playground", channel_id: "", name: "demo" },
      { kind: "playground", channel_id: "ch", name: "demo" },
    ]);
    expect(authOf(fetchMock.mock.calls[0]![1])).toBe("Bearer tok");
  });

  it("mints again before the link expires and after a clear", async () => {
    const { api, fetchMock } = await setup();
    const now = Date.now();
    const clock = vi.spyOn(Date, "now").mockReturnValue(now);
    const scope = { kind: "raw", channelId: "ch", root: 0 } as const;
    await expect(api.contentCapBase(scope)).resolves.toBe("http://api/c/cap1/");
    clock.mockReturnValue(now + 81_000); // past 80% of 100s
    await expect(api.contentCapBase(scope)).resolves.toBe("http://api/c/cap2/");
    api.clearContentCaps();
    await expect(api.contentCapBase(scope)).resolves.toBe("http://api/c/cap3/");
    expect(fetchMock).toHaveBeenCalledTimes(3);
    clock.mockRestore();
  });

  it("forgets one scope's link and leaves the others", async () => {
    const { api, fetchMock } = await setup();
    const raw = { kind: "raw", channelId: "ch", root: 0 } as const;
    const pg = { kind: "playground", name: "demo" } as const;
    await expect(api.contentCapBase(raw)).resolves.toBe("http://api/c/cap1/");
    await expect(api.contentCapBase(pg)).resolves.toBe("http://api/c/cap2/");
    api.forgetContentCap(raw);
    await expect(api.contentCapBase(raw)).resolves.toBe("http://api/c/cap3/");
    await expect(api.contentCapBase(pg)).resolves.toBe("http://api/c/cap2/");
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("bumps the epoch and tells subscribers on a clear", async () => {
    const { api } = await setup();
    const listener = vi.fn();
    const unsubscribe = api.subscribeContentCaps(listener);
    const before = api.contentCapsEpoch();
    api.clearContentCaps();
    expect(api.contentCapsEpoch()).toBe(before + 1);
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
    api.clearContentCaps();
    expect(api.contentCapsEpoch()).toBe(before + 2);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("fails, and tries again on the next ask", async () => {
    const { api, fetchMock } = await setup(500);
    const scope = { kind: "playground", name: "demo" } as const;
    await expect(api.contentCapBase(scope)).rejects.toThrow("Failed to get a content link");
    await expect(api.contentCapBase(scope)).rejects.toThrow("Failed to get a content link");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

describe("encodePathSegments", () => {
  it("encodes each segment and keeps the slashes", () => {
    return load().then((api) => expect(api.encodePathSegments("a b/#1/c?.png")).toBe("a%20b/%231/c%3F.png"));
  });
});
