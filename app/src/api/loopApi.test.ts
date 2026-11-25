import { describe, expect, it } from "vitest";

import * as api from "./api";
import * as channels from "./channels";
import * as loopApi from "./loopApi";
import * as tasks from "./tasks";
import * as workflows from "./workflows";

describe("loopApi", () => {
  it("re-exports the api modules", () => {
    expect(loopApi.apiFetch).toBe(api.apiFetch);
    expect(loopApi.fetchTasks).toBe(tasks.fetchTasks);
    expect(loopApi.fetchWorkflows).toBe(workflows.fetchWorkflows);
    expect(Object.keys(loopApi)).toEqual(expect.arrayContaining(Object.keys(channels)));
  });
});
