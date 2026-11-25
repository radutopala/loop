import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { API, type FetchMock, fail, json, sent, stubFetch } from "../test/hooks-c-api-fetch";
import { createTask, deleteTask, fetchAllTasks, fetchTaskRuns, fetchTasks, runTaskNow, updateTask } from "./tasks";

let fetchMock: FetchMock;

beforeEach(() => {
  fetchMock = stubFetch();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("tasks", () => {
  it("lists a channel's tasks", async () => {
    fetchMock.mockResolvedValue(json([{ id: 1 }]));
    await expect(fetchTasks("c 1")).resolves.toEqual([{ id: 1 }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/tasks?channel_id=c%201`);
  });

  it("lists every local task", async () => {
    fetchMock.mockResolvedValue(json([{ id: 2 }]));
    await expect(fetchAllTasks()).resolves.toEqual([{ id: 2 }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/tasks?platform=local`);
  });

  it("creates a task", async () => {
    fetchMock.mockResolvedValue(json({ id: 7 }));
    const data = { channel_id: "c1", schedule: "0 * * * *", type: "cron", prompt: "hi" };
    await expect(createTask(data)).resolves.toEqual({ id: 7 });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tasks`, method: "POST", body: data });
    expect(sent(fetchMock).headers.get("Content-Type")).toBe("application/json");
  });

  it("updates a task", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await updateTask(7, { enabled: false });
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tasks/7`, method: "PATCH", body: { enabled: false } });
  });

  it("deletes a task", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await deleteTask(7);
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tasks/7`, method: "DELETE" });
  });

  it("lists a task's runs", async () => {
    fetchMock.mockResolvedValue(json([{ id: 1, status: "success" }]));
    await expect(fetchTaskRuns(7)).resolves.toEqual([{ id: 1, status: "success" }]);
    expect(sent(fetchMock).url).toBe(`${API}/api/tasks/7/runs`);
  });

  it("returns no runs for a null body", async () => {
    fetchMock.mockResolvedValue(json(null));
    await expect(fetchTaskRuns(7)).resolves.toEqual([]);
  });

  it("runs a task now", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 202 }));
    await runTaskNow(7);
    expect(sent(fetchMock)).toMatchObject({ url: `${API}/api/tasks/7/run`, method: "POST" });
  });

  it("says when the task is already running", async () => {
    fetchMock.mockResolvedValue(fail(409, "Conflict"));
    await expect(runTaskNow(7)).rejects.toThrow("Task is already running");
  });

  it.each([
    ["fetchTasks", () => fetchTasks("c1"), "Failed to fetch tasks: boom"],
    ["fetchAllTasks", () => fetchAllTasks(), "Failed to fetch tasks: boom"],
    ["createTask", () => createTask({ channel_id: "c", schedule: "", type: "once", prompt: "" }), "Failed to create task: boom"],
    ["updateTask", () => updateTask(1, {}), "Failed to update task: boom"],
    ["deleteTask", () => deleteTask(1), "Failed to delete task: boom"],
    ["fetchTaskRuns", () => fetchTaskRuns(1), "Failed to fetch task runs: boom"],
    ["runTaskNow", () => runTaskNow(1), "Failed to run task: boom"],
  ])("%s throws on an error status", async (_name, run, msg) => {
    fetchMock.mockResolvedValue(fail(500, "boom"));
    await expect(run()).rejects.toThrow(msg);
  });
});
