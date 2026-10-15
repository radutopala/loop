import { describe, expect, it, vi } from "vitest";
import type { SideChat, SideChatMode } from "../api/sideChats";
import type { CanvasNode } from "../canvas/types";
import type { PaneNode } from "../types/panels";
import { type PaneSideChats, type SideChatDeps, SideChats, sideChatLeafIds, sideChatsKey } from "./sideChats";

// fakeDeps is a daemon and a storage in memory: create hands out side-1,
// side-2, …, and list returns the side chats not removed.
function fakeDeps(listed: string[] = []) {
  const stored = new Map<string, PaneSideChats>();
  const server = new Set(listed);
  let n = 0;
  const deps = {
    create: vi.fn(async (_channelId: string, _mode: SideChatMode) => {
      const id = `side-${++n}`;
      server.add(id);
      return id;
    }),
    list: vi.fn(async (): Promise<SideChat[]> => [...server].map((channel_id) => ({ channel_id }))),
    remove: vi.fn(async (id: string) => {
      server.delete(id);
    }),
    load: (channelId: string) => stored.get(channelId) ?? {},
    save: (channelId: string, panes: PaneSideChats) => stored.set(channelId, panes),
  } satisfies SideChatDeps;
  return { deps, stored, server };
}

describe("sideChatsKey", () => {
  it("keys the channel", () => {
    expect(sideChatsKey("c1")).toBe("side-chats:c1");
  });
});

describe("sideChatLeafIds", () => {
  it("collects the chat-side panes of split and canvas layouts", () => {
    const split: PaneNode = {
      type: "split",
      direction: "horizontal",
      flex: 1,
      children: [
        { type: "leaf", id: "chat", panel: "chat", flex: 1 },
        { type: "leaf", id: "chat-side-0", panel: "chat-side", flex: 1, openMode: "fork" },
      ],
    };
    const canvas: CanvasNode = {
      type: "canvas",
      viewport: { x: 0, y: 0, zoom: 1 },
      tiles: [
        { id: "chat-side-t1", panel: "chat-side", x: 0, y: 0, width: 1, height: 1, zIndex: 0, openMode: "fresh" },
        { id: "editor-t1", panel: "editor", x: 0, y: 0, width: 1, height: 1, zIndex: 0 },
      ],
    };
    expect(sideChatLeafIds([split, canvas])).toEqual(new Set(["chat-side-0", "chat-side-t1"]));
  });
});

describe("SideChats.open", () => {
  it("starts a pane's side chat once and keeps it", async () => {
    const { deps, stored } = fakeDeps();
    const sc = new SideChats(deps);
    await expect(sc.open("c1", "p1", "fresh")).resolves.toBe("side-1");
    await expect(sc.open("c1", "p1", "fresh")).resolves.toBe("side-1");
    expect(deps.create).toHaveBeenCalledTimes(1);
    expect(deps.create).toHaveBeenCalledWith("c1", "fresh");
    expect(stored.get("c1")).toEqual({ p1: "side-1" });
  });

  it("starts one side chat for a pane opened twice at once", async () => {
    const { deps } = fakeDeps();
    const sc = new SideChats(deps);
    const [a, b] = await Promise.all([sc.open("c1", "p1", "fork"), sc.open("c1", "p1", "fork")]);
    expect([a, b]).toEqual(["side-1", "side-1"]);
    expect(deps.create).toHaveBeenCalledTimes(1);
  });

  it("gives each pane its own side chat", async () => {
    const { deps, stored } = fakeDeps();
    const sc = new SideChats(deps);
    await sc.open("c1", "p1", "fork");
    await sc.open("c1", "p2", "fresh");
    expect(stored.get("c1")).toEqual({ p1: "side-1", p2: "side-2" });
  });

  it("keeps nothing when the side chat can't start, and tries again next time", async () => {
    const { deps, stored } = fakeDeps();
    deps.create.mockRejectedValueOnce(new Error("the channel has no session to fork"));
    const sc = new SideChats(deps);
    await expect(sc.open("c1", "p1", "fork")).rejects.toThrow("the channel has no session to fork");
    expect(stored.get("c1")).toBeUndefined();
    await expect(sc.open("c1", "p1", "fork")).resolves.toBe("side-1");
  });
});

describe("SideChats.close", () => {
  it("ends the pane's side chat and forgets it", async () => {
    const { deps, stored, server } = fakeDeps();
    const sc = new SideChats(deps);
    await sc.open("c1", "p1", "fork");
    await sc.open("c1", "p2", "fork");
    await sc.close("c1", "p1");
    expect(deps.remove).toHaveBeenCalledWith("side-1");
    expect(stored.get("c1")).toEqual({ p2: "side-2" });
    expect([...server]).toEqual(["side-2"]);
  });

  it("does nothing for a pane with no side chat", async () => {
    const { deps } = fakeDeps();
    await new SideChats(deps).close("c1", "p1");
    expect(deps.remove).not.toHaveBeenCalled();
  });

  it("ends a side chat still starting once it has", async () => {
    const { deps, server } = fakeDeps();
    const sc = new SideChats(deps);
    const opening = sc.open("c1", "p1", "fork");
    await sc.close("c1", "p1");
    await expect(opening).resolves.toBe("side-1");
    expect(deps.remove).toHaveBeenCalledWith("side-1");
    expect(server.size).toBe(0);
  });

  it("does nothing when the side chat couldn't start", async () => {
    const { deps } = fakeDeps();
    deps.create.mockRejectedValueOnce(new Error("no"));
    const sc = new SideChats(deps);
    const opening = sc.open("c1", "p1", "fork").catch(() => undefined);
    await sc.close("c1", "p1");
    await opening;
    expect(deps.remove).not.toHaveBeenCalled();
  });
});

describe("SideChats.sweep", () => {
  it("ends the side chats no pane shows and forgets the panes gone", async () => {
    const { deps, stored, server } = fakeDeps(["orphan"]);
    const sc = new SideChats(deps);
    await sc.open("c1", "kept", "fork");
    await sc.open("c1", "gone", "fresh");
    await expect(sc.sweep("c1", () => new Set(["kept"]))).resolves.toEqual(["orphan", "side-2"]);
    expect([...server]).toEqual(["side-1"]);
    expect(stored.get("c1")).toEqual({ kept: "side-1" });
  });

  it("forgets a pane's side chat that's gone already", async () => {
    const { deps, stored, server } = fakeDeps();
    const sc = new SideChats(deps);
    await sc.open("c1", "p1", "fork");
    server.clear();
    await expect(sc.sweep("c1", () => new Set(["p1"]))).resolves.toEqual([]);
    expect(stored.get("c1")).toEqual({});
  });

  it("keeps a side chat starting while it lists", async () => {
    const { deps, server } = fakeDeps();
    let release: () => void = () => {};
    deps.create.mockImplementationOnce(async () => {
      server.add("side-new");
      await new Promise<void>((r) => {
        release = r;
      });
      return "side-new";
    });
    const sc = new SideChats(deps);
    const opening = sc.open("c1", "p1", "fork");
    const sweeping = sc.sweep("c1", () => new Set(["p1"]));
    await Promise.resolve();
    release();
    await opening;
    await expect(sweeping).resolves.toEqual([]);
    expect([...server]).toEqual(["side-new"]);
  });

  it("leaves the stored panes as they are when nothing changed", async () => {
    const { deps } = fakeDeps();
    const save = vi.spyOn(deps, "save");
    const sc = new SideChats(deps);
    await sc.open("c1", "p1", "fork");
    save.mockClear();
    await expect(sc.sweep("c1", () => new Set(["p1"]))).resolves.toEqual([]);
    expect(save).not.toHaveBeenCalled();
  });
});
