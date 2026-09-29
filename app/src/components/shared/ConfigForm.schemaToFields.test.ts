import { describe, expect, it } from "vitest";
import type { ConfigSchema } from "../../api/configApi";
import { schemaToFields } from "./ConfigForm";

const schema: ConfigSchema = {
  type: "object",
  properties: {
    mounts: { type: "array", items: { type: "string" }, "x-section": "Workspace", "x-order": 2 },
    inherit_mounts: { type: "boolean", default: true, "x-section": "Workspace", "x-order": 3, "x-project-only": true },
    log_level: { type: "string", "x-section": "General", "x-global-only": true },
  },
};

describe("schemaToFields", () => {
  it("hides project-only fields from the global config", () => {
    expect(schemaToFields(schema, true).map((f) => f.key)).toEqual(["mounts", "log_level"]);
  });

  it("hides global-only fields from a project config", () => {
    const fields = schemaToFields(schema, false);
    expect(fields.map((f) => f.key)).toEqual(["mounts", "inherit_mounts"]);
    expect(fields[1]).toMatchObject({ type: "toggle", defaultValue: true });
  });
});
