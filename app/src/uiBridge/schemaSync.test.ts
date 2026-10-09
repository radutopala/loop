import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { PANEL_OPTIONS } from "../types/panels";

// The MCP ui_run tool lists the ops and panels in its schema, in Go; a step
// the schema refuses never reaches the window.
const goSource = readFileSync(new URL("../../../internal/mcpserver/ui.go", import.meta.url), "utf8");
const executorSource = readFileSync(new URL("./executor.ts", import.meta.url), "utf8");

function goList(name: string): string[] {
  const block = new RegExp(`${name} = \\[\\]any\\{([^}]*)\\}`).exec(goSource);
  if (!block) throw new Error(`no ${name} in ui.go`);
  return [...(block[1] as string).matchAll(/"([^"]+)"/g)].map((m) => m[1] as string).sort();
}

describe("the ui_run schema", () => {
  it("lists the executor's ops", () => {
    const ops = [...executorSource.matchAll(/case "([a-z_]+)":/g)].map((m) => m[1] as string).sort();
    expect(goList("uiOps")).toEqual(ops);
  });

  it("lists the panels", () => {
    expect(goList("uiPanels")).toEqual(PANEL_OPTIONS.map((o) => o.panel).sort());
  });
});
