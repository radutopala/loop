import { describe, expect, it } from "vitest";
import { shouldForwardToChatListeners } from "./chatEventRouting";

describe("shouldForwardToChatListeners", () => {
  const cases: { name: string; type: string; stateTarget: string; selectedId: string | null; want: boolean }[] = [
    // The ordinary rule: a channel-scoped event belongs to the open view only.
    { name: "channel event for the selected channel", type: "message.created", stateTarget: "ch1", selectedId: "ch1", want: true },
    { name: "channel event for a background channel", type: "message.created", stateTarget: "ch2", selectedId: "ch1", want: false },
    { name: "channel event with nothing selected", type: "message.created", stateTarget: "ch1", selectedId: null, want: false },
    // A thread-routed agent.status arrives with stateTarget = the thread, so
    // the parent view stays quiet.
    { name: "thread-routed event while the parent is open", type: "agent.status", stateTarget: "thread1", selectedId: "ch1", want: false },
    // Globally-broadcast workflow events carry no channel_id at all. Before
    // the exception these were dropped, and the Review panel never saw the
    // run finish — Run review and the chat composer stayed disabled.
    { name: "workflow.run_completed with an empty channel", type: "workflow.run_completed", stateTarget: "", selectedId: "ch1", want: true },
    { name: "workflow.run_started with an empty channel", type: "workflow.run_started", stateTarget: "", selectedId: "ch1", want: true },
    { name: "workflow.node_started with an empty channel", type: "workflow.node_started", stateTarget: "", selectedId: "ch1", want: true },
    { name: "workflow.run_paused with nothing selected", type: "workflow.run_paused", stateTarget: "", selectedId: null, want: true },
    // Other global events stay filtered — subscribers can't tell whose they
    // are, and unlike workflow.* they carry no run_id to filter on.
    { name: "a non-workflow global event", type: "channel.created", stateTarget: "", selectedId: "ch1", want: false },
    // "workflow" is only special as a prefix, not as a substring.
    { name: "an event merely mentioning workflow", type: "task.workflow_hint", stateTarget: "", selectedId: "ch1", want: false },
  ];

  for (const c of cases) {
    it(c.name, () => {
      expect(shouldForwardToChatListeners(c.type, c.stateTarget, c.selectedId)).toBe(c.want);
    });
  }
});
