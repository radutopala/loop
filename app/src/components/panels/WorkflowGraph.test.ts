import { describe, expect, it } from "vitest";
import { expandLoopBodies } from "./WorkflowGraph";
import type { WorkflowNodeDef, WorkflowNodeRun } from "../../api/workflows";

function def(id: string, type: WorkflowNodeDef["type"], extra: Partial<WorkflowNodeDef> = {}): WorkflowNodeDef {
  return { id, type, ...extra };
}

function run(nodeId: string, iteration: number): WorkflowNodeRun {
  return {
    id: 0,
    run_id: "r",
    node_id: nodeId,
    iteration,
    status: "success",
    input: "",
    session_id: "",
    output: "",
    error_text: "",
    attempt: 1,
    started_at: null,
    finished_at: null,
    last_heartbeat_at: null,
  };
}

const synth = (loopId: string, iter: number, child: string) => `${loopId}:iter${iter}:${child}`;
const idsOf = (defs: WorkflowNodeDef[]) => defs.map((d) => d.id);

describe("expandLoopBodies", () => {
  it("passes non-loop and empty-body defs through untouched", () => {
    const defs = [def("a", "prompt"), def("loopEmpty", "loop"), def("b", "bash", { depends_on: ["a"] })];
    const { effectiveDefs, groupSpecs } = expandLoopBodies(defs, []);
    expect(effectiveDefs).toEqual(defs);
    expect(groupSpecs).toEqual([]);
  });

  it("falls back to one iteration when no runs exist yet", () => {
    const defs = [
      def("L", "loop", { body: [def("review", "bash"), def("fix", "prompt", { depends_on: ["review"] })] }),
    ];
    const { effectiveDefs, groupSpecs } = expandLoopBodies(defs, []);
    expect(idsOf(effectiveDefs)).toEqual([synth("L", 0, "review"), synth("L", 0, "fix")]);
    // The loop container def itself is dropped.
    expect(idsOf(effectiveDefs)).not.toContain("L");
    expect(groupSpecs).toEqual([
      { loopId: "L", iterCount: 1, syntheticIds: [synth("L", 0, "review"), synth("L", 0, "fix")] },
    ]);
  });

  it("derives iteration count from max observed run iteration + 1", () => {
    const defs = [def("L", "loop", { body: [def("review", "bash"), def("fix", "prompt")] })];
    const runs = [run("review", 0), run("fix", 0), run("review", 1), run("fix", 1), run("review", 2)];
    const { effectiveDefs, groupSpecs } = expandLoopBodies(defs, runs);
    expect(groupSpecs[0]!.iterCount).toBe(3);
    // 2 children × 3 iters = 6 synthetic defs.
    expect(effectiveDefs).toHaveLength(6);
  });

  it("rewrites intra-body deps to the same iteration's synthetic ids", () => {
    const defs = [
      def("L", "loop", { body: [def("review", "bash"), def("fix", "prompt", { depends_on: ["review"] })] }),
    ];
    const { effectiveDefs } = expandLoopBodies(defs, [run("review", 0), run("fix", 0)]);
    const fix0 = effectiveDefs.find((d) => d.id === synth("L", 0, "fix"))!;
    expect(fix0.depends_on).toEqual([synth("L", 0, "review")]);
  });

  it("chains the first child of iteration N to the last child of N-1", () => {
    const defs = [def("L", "loop", { body: [def("review", "bash"), def("fix", "prompt", { depends_on: ["review"] })] })];
    const runs = [run("review", 0), run("fix", 0), run("review", 1), run("fix", 1)];
    const { effectiveDefs } = expandLoopBodies(defs, runs);
    const review1 = effectiveDefs.find((d) => d.id === synth("L", 1, "review"))!;
    // iteration 1's first child (review) depends on iteration 0's last child (fix).
    expect(review1.depends_on).toContain(synth("L", 0, "fix"));
  });

  it("keeps an external (pre-loop) dep on a body child", () => {
    const defs = [
      def("setup", "bash"),
      def("L", "loop", { body: [def("review", "bash", { depends_on: ["setup"] })] }),
    ];
    const { effectiveDefs } = expandLoopBodies(defs, [run("review", 0)]);
    const review0 = effectiveDefs.find((d) => d.id === synth("L", 0, "review"))!;
    expect(review0.depends_on).toContain("setup");
  });

  it("rewires a top-level dep on the loop id to the loop's final emitted node", () => {
    const defs = [
      def("L", "loop", { body: [def("review", "bash"), def("fix", "prompt")] }),
      def("after", "bash", { depends_on: ["L"] }),
    ];
    const runs = [run("review", 0), run("fix", 0), run("review", 1), run("fix", 1)];
    const { effectiveDefs } = expandLoopBodies(defs, runs);
    const after = effectiveDefs.find((d) => d.id === "after")!;
    // Last iteration is 1, last child is fix → after depends on L:iter1:fix.
    expect(after.depends_on).toEqual([synth("L", 1, "fix")]);
  });

  it("does not rewire deps that don't reference a loop id", () => {
    const defs = [
      def("a", "bash"),
      def("L", "loop", { body: [def("x", "bash")] }),
      def("b", "bash", { depends_on: ["a"] }),
    ];
    const { effectiveDefs } = expandLoopBodies(defs, [run("x", 0)]);
    expect(effectiveDefs.find((d) => d.id === "b")!.depends_on).toEqual(["a"]);
  });
});
