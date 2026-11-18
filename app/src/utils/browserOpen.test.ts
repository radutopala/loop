import { describe, expect, it } from "vitest";

import { BROWSER_OPEN_EVENT, BrowserOpenQueue } from "./browserOpen";

function recorder() {
  const target = new EventTarget();
  const events: { type: string; detail: unknown }[] = [];
  for (const type of ["loop:open-panel", BROWSER_OPEN_EVENT]) {
    target.addEventListener(type, (e) => events.push({ type, detail: (e as CustomEvent).detail }));
  }
  return { target, events };
}

describe("BrowserOpenQueue", () => {
  it.each([
    ["docker", "docker-browser"],
    ["host", "host-browser"],
  ] as const)("asks for the %s pane beside the chat, then wakes it", (mode, panel) => {
    const { target, events } = recorder();
    new BrowserOpenQueue().request(target, "ch1", mode, "https://example.test/x");
    expect(events).toEqual([
      { type: "loop:open-panel", detail: { channelId: "ch1", panel, anchorPanel: "chat" } },
      { type: BROWSER_OPEN_EVENT, detail: { channelId: "ch1", mode } },
    ]);
  });

  it("hands each pane its own links, in order, once", () => {
    const { target } = recorder();
    const q = new BrowserOpenQueue();
    q.request(target, "ch1", "docker", "https://example.test/a");
    q.request(target, "ch1", "host", "https://example.test/b");
    q.request(target, "ch2", "docker", "https://example.test/c");
    q.request(target, "ch1", "docker", "https://example.test/d");
    expect(q.take("ch1", "docker")).toEqual(["https://example.test/a", "https://example.test/d"]);
    expect(q.take("ch1", "docker")).toEqual([]);
    expect(q.take("ch1", "host")).toEqual(["https://example.test/b"]);
    expect(q.take("ch2", "docker")).toEqual(["https://example.test/c"]);
  });
});
