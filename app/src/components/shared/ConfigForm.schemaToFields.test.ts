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

  it("carries a dropdown's option labels", () => {
    const fields = schemaToFields({ type: "object", properties: { lang: { type: "string", enum: ["auto", "ro"], "x-enum-labels": { ro: "Romanian (ro)" }, "x-section": "Dictation" } } }, true);
    expect(fields[0]).toMatchObject({ type: "dropdown", options: ["auto", "ro"], optionLabels: { ro: "Romanian (ro)" } });
  });
});
