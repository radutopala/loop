import { describe, expect, it } from "vitest";
import type { Channel, ChannelUpdatedData } from "../types";
import { applyChannelUpdate } from "./channelUpdate";

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

  it("clears the description", () => {
    expect(applyChannelUpdate(channel, { ...gitOnly, description: "" }).description).toBe("");
  });
});
