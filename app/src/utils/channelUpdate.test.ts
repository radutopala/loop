import { describe, expect, it } from "vitest";
import type { Channel, ChannelUpdatedData } from "../types";
import { applyChannelUpdate, applyReviewStatus, replayChannelPatches } from "./channelUpdate";

const channel: Channel = {
  id: "t1",
  name: "thread",
  dir_path: "/w",
  branch: "feat/x",
  commit: "abc1234",
  subject: "wip",
  description: "fixes login",
} as Channel;

const gitOnly: ChannelUpdatedData = { channel_id: "t1", branch: "", commit: "", diff_additions: 0, diff_deletions: 0 };

describe("applyReviewStatus", () => {
  it.each([
    ["reviewing", true],
    ["ready", false],
    ["idle", false],
    ["error", false],
  ])("%s marks the review running: %s", (status, running) => {
    expect(applyReviewStatus(channel, status, 5).review_running).toBe(running);
  });

  it("dates the channel's activity, never back", () => {
    expect(applyReviewStatus(channel, "reviewing", 5).last_activity_at).toBe(5);
    expect(applyReviewStatus({ ...channel, last_activity_at: 9 }, "ready", 5).last_activity_at).toBe(9);
  });
});

describe("applyChannelUpdate", () => {
  it("applies the poller's git state", () => {
    const got = applyChannelUpdate(channel, { ...gitOnly, branch: "main", commit: "def5678", subject: "done" });
    expect(got).toMatchObject({ branch: "main", commit: "def5678", subject: "done", name: "thread", description: "fixes login" });
  });

  it("renames without clearing the git state", () => {
    const got = applyChannelUpdate(channel, { ...gitOnly, name: "renamed" });
    expect(got).toMatchObject({ name: "renamed", branch: "feat/x", commit: "abc1234", subject: "wip", description: "fixes login" });
  });

  it("sets a description without clearing the git state", () => {
    const got = applyChannelUpdate(channel, { ...gitOnly, description: "reviews PRs" });
    expect(got).toMatchObject({ description: "reviews PRs", name: "thread", branch: "feat/x", commit: "abc1234" });
  });

  it("sets a ticket URL without clearing the git state or description", () => {
    const got = applyChannelUpdate(channel, { ...gitOnly, ticket_url: "https://x.com/T-1" });
    expect(got).toMatchObject({ ticket_url: "https://x.com/T-1", description: "fixes login", branch: "feat/x", commit: "abc1234" });
  });

  it("clears the ticket URL", () => {
    expect(applyChannelUpdate({ ...channel, ticket_url: "https://x.com/T-1" }, { ...gitOnly, ticket_url: "" }).ticket_url).toBe("");
  });

  it("clears the description", () => {
    expect(applyChannelUpdate(channel, { ...gitOnly, description: "" }).description).toBe("");
  });
});

describe("replayChannelPatches", () => {
  const other = { ...channel, id: "t2", name: "other" } as Channel;
  const ticket = (url: string) => (c: Channel) => ({ ...c, ticket_url: url });

  it("applies patches newer than the fetch, in arrival order, to their channel only", () => {
    const patches = [
      { at: 5, id: "t1", apply: ticket("https://old.example/1") },
      { at: 15, id: "t1", apply: ticket("https://new.example/1") },
      { at: 20, id: "t1", apply: ticket("https://newest.example/1") },
    ];
    const got = replayChannelPatches([channel, other], patches, 10);
    expect(got.channels[0]?.ticket_url).toBe("https://newest.example/1");
    expect(got.channels[1]).toBe(other);
    expect(got.pending.map((p) => p.at)).toEqual([15, 20]);
  });

  it("returns the fetched list untouched and drops patches the fetch already saw", () => {
    const list = [channel];
    const got = replayChannelPatches(list, [{ at: 5, id: "t1", apply: ticket("https://x.example/1") }], 10);
    expect(got.channels).toBe(list);
    expect(got.pending).toEqual([]);
  });
});
