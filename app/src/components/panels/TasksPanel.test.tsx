import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ScheduledTask, TaskRunLog, WorkflowDef } from "../../api/loopApi";
import { createTask, deleteTask, fetchTaskRuns, fetchTasks, fetchWorkflows, runTaskNow, updateTask } from "../../api/loopApi";
import { fetchPluginList, PLUGINS_CHANGED, type PluginInfo, type PluginList, setPluginScheduleEntry } from "../../api/plugins";
import { useEventStream } from "../../hooks/useEventStream";
import type { WSEvent } from "../../types";
import { TasksPanel } from "./TasksPanel";

vi.mock("../../api/loopApi", () => ({
  createTask: vi.fn(),
  deleteTask: vi.fn(),
  fetchTaskRuns: vi.fn(),
  fetchTasks: vi.fn(),
  fetchWorkflows: vi.fn(),
  runTaskNow: vi.fn(),
  updateTask: vi.fn(),
}));

vi.mock("../../api/plugins", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/plugins")>()),
  fetchPluginList: vi.fn(),
  setPluginScheduleEntry: vi.fn(),
}));

vi.mock("../../hooks/useEventStream", () => ({
  useEventStream: vi.fn(),
}));

function task(o: Partial<ScheduledTask> = {}): ScheduledTask {
  return {
    id: 1,
    channel_id: "ch",
    schedule: "*/5 * * * *",
    type: "cron",
    prompt: "do the thing",
    enabled: true,
    next_run_at: "2026-01-01T00:00:00Z",
    auto_delete_sec: 0,
    worktree: false,
    update_before_run: false,
    running: false,
    ...o,
  };
}

function runLog(o: Partial<TaskRunLog> = {}): TaskRunLog {
  return { id: 1, task_id: 1, status: "success", response_text: "", error_text: "", started_at: new Date().toISOString(), finished_at: "", ...o };
}

function plugin(o: Partial<PluginInfo> = {}): PluginInfo {
  return {
    id: "pl",
    name: "Plug",
    version: "1",
    capabilities: ["schedule"],
    status: "running",
    enabled: true,
    actions: ["sync"],
    default_schedules: { sync: { type: "cron", schedule: "0 * * * *" } },
    ...o,
  };
}

function pluginList(o: Partial<PluginList> = {}, p: PluginInfo = plugin()): PluginList {
  return { off: false, plugins: [p], projectTrusted: true, ...o };
}

const workflows: WorkflowDef[] = [
  { name: "deploy", description: "", nodes: [], inputs: { env: { description: "where", required: true, default: "prod" }, tag: {} } },
  { name: "plain", description: "", nodes: [] },
];

type Props = Parameters<typeof TasksPanel>[0];

async function setup(tasks: ScheduledTask[] = [], props: Partial<Props> = {}) {
  vi.mocked(fetchTasks).mockResolvedValue(tasks);
  const user = userEvent.setup();
  const view = render(<TasksPanel channelId="ch" {...props} />);
  await screen.findByText(`${tasks.length} task${tasks.length === 1 ? "" : "s"}`);
  return { user, ...view };
}

function emit(type: string) {
  const { onEvent } = vi.mocked(useEventStream).mock.lastCall![0];
  act(() => onEvent({ type } as WSEvent));
}

const createBtn = () => screen.getByTestId("task-new-create");

beforeEach(() => {
  vi.mocked(fetchTasks).mockResolvedValue([]);
  vi.mocked(fetchWorkflows).mockResolvedValue(workflows);
  vi.mocked(fetchTaskRuns).mockResolvedValue([]);
  vi.mocked(fetchPluginList).mockResolvedValue(pluginList());
  vi.mocked(createTask).mockResolvedValue(task());
  vi.mocked(updateTask).mockResolvedValue(undefined);
  vi.mocked(deleteTask).mockResolvedValue(undefined);
  vi.mocked(runTaskNow).mockResolvedValue(undefined);
  vi.mocked(setPluginScheduleEntry).mockResolvedValue(undefined);
});

afterEach(() => {
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("TasksPanel list", () => {
  it("shows the empty state and swallows load errors", async () => {
    vi.mocked(fetchWorkflows).mockRejectedValue(new Error("no"));
    vi.mocked(fetchTasks).mockRejectedValue(new Error("down"));
    render(<TasksPanel channelId="ch" />);
    await waitFor(() => expect(fetchTasks).toHaveBeenCalledWith("ch"));
    expect(screen.getByText("0 tasks")).toBeTruthy();
    expect(screen.getByText("No scheduled tasks")).toBeTruthy();
    expect(screen.getByText("Select a task to view details")).toBeTruthy();
    expect(fetchWorkflows).toHaveBeenCalledWith("ch");
    // Plugins aren't listed unless the panel allows them.
    expect(fetchPluginList).not.toHaveBeenCalled();
    expect(useEventStream).toHaveBeenCalledWith(expect.objectContaining({ channelId: "ch" }));
  });

  it("renders each row's type, schedule and summary", async () => {
    await setup([
      task({ id: 1, type: "manual", schedule: "" }),
      task({ id: 2, worktree: true, enabled: false, prompt: "off one" }),
      task({ id: 3, type: "weird" as ScheduledTask["type"], schedule: "x y" }),
    ]);
    const row1 = screen.getByTestId("task-row-1");
    expect(within(row1).getByText("on demand")).toBeTruthy();
    // A manual task has no next run.
    expect(within(row1).queryByText(/Next:/)).toBeNull();
    const row2 = screen.getByTestId("task-row-2");
    expect(within(row2).getByText("wt")).toBeTruthy();
    expect(row2.style.opacity).toBe("0.5");
    expect(within(row2).getByTitle("Enable").textContent).toBe("○");
    expect(within(row2).queryByText(/Next:/)).toBeNull();
    const row3 = screen.getByTestId("task-row-3");
    expect(within(row3).getByText("x y")).toBeTruthy();
    expect(within(row3).getByText("Next: overdue")).toBeTruthy();
    expect(within(row3).getByTitle("Disable").textContent).toBe("●");
  });

  it("highlights a row on hover unless it is selected", async () => {
    const { user } = await setup([task({ id: 1 })]);
    const row = screen.getByTestId("task-row-1");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe("rgba(255, 255, 255, 0.04)");
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe("transparent");
    await user.click(row);
    const bg = row.style.background;
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe(bg);
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe(bg);
  });

  it("resizes the list by dragging the divider within bounds", async () => {
    await setup();
    const panel = screen.getByTestId("tasks-panel");
    const list = panel.children[0] as HTMLElement;
    const divider = panel.children[1] as HTMLElement;
    const move = (dx: number) => {
      const e = new MouseEvent("mousemove");
      Object.defineProperty(e, "movementX", { value: dx });
      act(() => {
        window.dispatchEvent(e);
      });
    };
    expect(list.style.width).toBe("340px");
    fireEvent.mouseDown(divider);
    move(60);
    expect(list.style.width).toBe("400px");
    move(1000);
    expect(list.style.width).toBe("500px");
    move(-1000);
    expect(list.style.width).toBe("200px");
    act(() => {
      window.dispatchEvent(new MouseEvent("mouseup"));
    });
    move(50);
    expect(list.style.width).toBe("200px");
  });
});

describe("TasksPanel live updates", () => {
  it("reloads tasks, runs and plugins on events", async () => {
    const { user } = await setup([task({ id: 7 })], { allowPlugins: true });
    expect(fetchPluginList).toHaveBeenCalledTimes(1);
    expect(fetchPluginList).toHaveBeenCalledWith("ch");

    emit("task.run_completed");
    await waitFor(() => expect(fetchTasks).toHaveBeenCalledTimes(2));
    // Nothing selected: runs aren't loaded.
    expect(fetchTaskRuns).not.toHaveBeenCalled();

    await user.click(screen.getByTestId("task-row-7"));
    await waitFor(() => expect(fetchTaskRuns).toHaveBeenCalledWith(7));
    emit("task.run_completed");
    await waitFor(() => expect(fetchTaskRuns).toHaveBeenCalledTimes(2));
    emit("task.updated");
    await waitFor(() => expect(fetchTasks).toHaveBeenCalledTimes(4));
    expect(fetchTaskRuns).toHaveBeenCalledTimes(2);

    emit("plugins.changed");
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalledTimes(2));
    act(() => {
      window.dispatchEvent(new Event(PLUGINS_CHANGED));
    });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalledTimes(3));

    emit("message.created");
    expect(fetchTasks).toHaveBeenCalledTimes(4);
  });

  it("ignores a failed plugin list and stops listening on unmount", async () => {
    vi.mocked(fetchPluginList).mockRejectedValue(new Error("off"));
    const { unmount } = await setup([], { allowPlugins: true });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalledTimes(1));
    unmount();
    window.dispatchEvent(new Event(PLUGINS_CHANGED));
    expect(fetchPluginList).toHaveBeenCalledTimes(1);
  });
});

describe("TasksPanel detail", () => {
  it("shows a prompt task with its run history", async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([
      runLog({ id: 1, status: "success", response_text: "x".repeat(250) }),
      runLog({ id: 2, status: "failed", error_text: "boom" }),
      runLog({ id: 3, status: "running" }),
    ]);
    const { user } = await setup([task({ id: 1, worktree: true, origin_branch: "main", update_before_run: true, auto_delete_sec: 60 })]);
    await user.click(screen.getByTestId("task-row-1"));
    expect(await screen.findByText("Task #1")).toBeTruthy();
    expect(screen.getByText("worktree")).toBeTruthy();
    expect(screen.getAllByText("do the thing").length).toBe(2);
    expect(screen.getByText("Next run: overdue")).toBeTruthy();
    expect(screen.getByText("Branch: main (updates before run)")).toBeTruthy();
    expect(screen.getByText("Auto-delete after 60s")).toBeTruthy();
    expect(await screen.findByText("x".repeat(200))).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
    expect(screen.getByText("running")).toBeTruthy();
    expect(screen.getByText("failed")).toBeTruthy();
  });

  it("shows a workflow task's inputs and a bash task's script", async () => {
    const { user } = await setup([
      task({ id: 1, workflow_name: "deploy", workflow_inputs: '{"env":"qa"}', worktree: true, origin_branch: "dev" }),
      task({ id: 2, workflow_name: "plain", workflow_inputs: "{}" }),
      task({ id: 3, bash_script: "echo hi", type: "manual", schedule: "", enabled: false }),
      task({ id: 4, type: "weird" as ScheduledTask["type"], worktree: true }),
    ]);
    await user.click(screen.getByTestId("task-row-1"));
    expect(await screen.findByText('Inputs: {"env":"qa"}')).toBeTruthy();
    expect(screen.getByText("Branch: dev")).toBeTruthy();
    expect(screen.getByText("No runs yet")).toBeTruthy();

    await user.click(screen.getByTestId("task-row-2"));
    expect(await screen.findByText("Task #2")).toBeTruthy();
    expect(screen.queryByText(/Inputs:/)).toBeNull();

    await user.click(screen.getByTestId("task-row-3"));
    expect(await screen.findByText("Task #3")).toBeTruthy();
    expect(screen.getByText("echo hi", { selector: "pre" })).toBeTruthy();
    expect(screen.getByText("on demand (run manually)")).toBeTruthy();
    expect(screen.queryByText(/Next run:/)).toBeNull();
    expect(screen.getByRole("button", { name: "Enable" })).toBeTruthy();

    await user.click(screen.getByTestId("task-row-4"));
    expect(await screen.findByText("Task #4")).toBeTruthy();
    // A worktree without an origin branch shows no branch line.
    expect(screen.queryByText(/Branch:/)).toBeNull();
  });

  it("ignores a failed run-history load", async () => {
    vi.mocked(fetchTaskRuns).mockRejectedValue(new Error("x"));
    const { user } = await setup([task()]);
    await user.click(screen.getByTestId("task-row-1"));
    await waitFor(() => expect(fetchTaskRuns).toHaveBeenCalledWith(1));
    expect(screen.getByText("No runs yet")).toBeTruthy();
  });

  it("describes a plugin action's task", async () => {
    const { user } = await setup([task({ plugin_id: "pl", plugin_action: "sync" })]);
    await user.click(screen.getByTestId("task-row-1"));
    expect((await screen.findByTestId("task-detail-plugin")).textContent).toContain("pl's sync action");
  });
});

describe("TasksPanel run now", () => {
  it("starts a run and marks the task running", async () => {
    let resolve!: () => void;
    vi.mocked(runTaskNow).mockReturnValue(new Promise<void>((r) => (resolve = r)));
    const { user } = await setup([task({ id: 5 }), task({ id: 6 })]);
    await user.click(screen.getByTestId("task-row-5"));
    await user.click(await screen.findByRole("button", { name: "▶ Run Now" }));
    expect(runTaskNow).toHaveBeenCalledWith(5);
    const starting = screen.getByRole("button", { name: "Starting..." }) as HTMLButtonElement;
    expect(starting.disabled).toBe(true);
    await act(async () => resolve());
    const running = screen.getByRole("button", { name: "Running..." }) as HTMLButtonElement;
    expect(running.disabled).toBe(true);
    expect(fetchTaskRuns).toHaveBeenCalledTimes(2);
    // Only the started task is marked running.
    await user.click(screen.getByTestId("task-row-6"));
    expect(await screen.findByRole("button", { name: "▶ Run Now" })).toBeTruthy();
  });

  it("returns to Run Now when the run fails to start", async () => {
    vi.mocked(runTaskNow).mockRejectedValue(new Error("busy"));
    const { user } = await setup([task({ id: 5 })]);
    await user.click(screen.getByTestId("task-row-5"));
    await user.click(await screen.findByRole("button", { name: "▶ Run Now" }));
    expect(await screen.findByRole("button", { name: "▶ Run Now" })).toBeTruthy();
  });

  it("opens a running task's thread", async () => {
    const onSelectChannel = vi.fn();
    const { user } = await setup([task({ id: 5, running: true, thread_id: "th1" })], { onSelectChannel });
    await user.click(screen.getByTestId("task-row-5"));
    await user.click(await screen.findByRole("button", { name: "Running..." }));
    expect(onSelectChannel).toHaveBeenCalledWith("th1");
  });

  it("does nothing on a running task's thread without a handler", async () => {
    const { user } = await setup([task({ id: 5, running: true, thread_id: "th1" })]);
    await user.click(screen.getByTestId("task-row-5"));
    await user.click(await screen.findByRole("button", { name: "Running..." }));
    expect(runTaskNow).not.toHaveBeenCalled();
  });
});

describe("TasksPanel toggle and delete", () => {
  it("toggles a task from its row without selecting it", async () => {
    const { user } = await setup([task({ id: 3 })]);
    await user.click(screen.getByTitle("Disable"));
    expect(updateTask).toHaveBeenCalledWith(3, { enabled: false });
    await waitFor(() => expect(fetchTasks).toHaveBeenCalledTimes(2));
    expect(screen.getByText("Select a task to view details")).toBeTruthy();
  });

  it("toggles from the detail and ignores a failed update", async () => {
    vi.mocked(updateTask).mockRejectedValue(new Error("no"));
    const { user } = await setup([task({ id: 3, enabled: false })]);
    await user.click(screen.getByTestId("task-row-3"));
    await user.click(screen.getByRole("button", { name: "Enable" }));
    expect(updateTask).toHaveBeenCalledWith(3, { enabled: true });
    expect(fetchTasks).toHaveBeenCalledTimes(1);
  });

  it("toggles a plugin task through its config entry", async () => {
    const { user } = await setup([task({ id: 3, plugin_id: "pl", plugin_action: "sync", schedule: "0 * * * *" })], { allowPlugins: true });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalled());
    await user.click(screen.getByTitle("Disable"));
    await waitFor(() => expect(setPluginScheduleEntry).toHaveBeenCalled());
    expect(setPluginScheduleEntry).toHaveBeenCalledWith("pl", "sync", { enabled: false, type: undefined, schedule: undefined }, "ch");
    expect(updateTask).not.toHaveBeenCalled();
    await waitFor(() => expect(fetchTasks).toHaveBeenCalledTimes(2));
  });

  it("toggles a plugin task directly when its plugin isn't on here", async () => {
    const { user } = await setup([task({ id: 3, plugin_id: "gone", plugin_action: "sync" })], { allowPlugins: true });
    await user.click(screen.getByTitle("Disable"));
    expect(updateTask).toHaveBeenCalledWith(3, { enabled: false });
    expect(setPluginScheduleEntry).not.toHaveBeenCalled();
  });

  it("deletes the selected task and clears the selection", async () => {
    const { user } = await setup([task({ id: 4 })]);
    await user.click(screen.getByTestId("task-row-4"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteTask).toHaveBeenCalledWith(4);
    expect(await screen.findByText("Select a task to view details")).toBeTruthy();
    expect(fetchTasks).toHaveBeenCalledTimes(2);
  });

  it("keeps the selection when a delete fails", async () => {
    vi.mocked(deleteTask).mockRejectedValue(new Error("no"));
    const { user } = await setup([task({ id: 4 })]);
    await user.click(screen.getByTestId("task-row-4"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteTask).toHaveBeenCalledWith(4);
    expect(screen.getByText("Task #4")).toBeTruthy();
  });

  it("removes a project's own plugin entry to delete its task", async () => {
    vi.mocked(fetchPluginList).mockResolvedValue(pluginList({}, plugin({ project_schedules: { sync: { enabled: true } } })));
    const { user } = await setup([task({ id: 4, plugin_id: "pl", plugin_action: "sync" })], { allowPlugins: true });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalled());
    await user.click(screen.getByTestId("task-row-4"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(setPluginScheduleEntry).toHaveBeenCalledWith("pl", "sync", undefined, "ch");
    expect(deleteTask).not.toHaveBeenCalled();
    expect(await screen.findByText("Select a task to view details")).toBeTruthy();
  });

  it("keeps the task selected when removing its entry fails", async () => {
    vi.mocked(fetchPluginList).mockResolvedValue(pluginList({}, plugin({ project_schedules: { sync: { enabled: true } } })));
    vi.mocked(setPluginScheduleEntry).mockRejectedValue("nope");
    const { user } = await setup([task({ id: 4, plugin_id: "pl", plugin_action: "sync" })], { allowPlugins: true });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalled());
    await user.click(screen.getByTestId("task-row-4"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(setPluginScheduleEntry).toHaveBeenCalled());
    expect(screen.getByText("Task #4")).toBeTruthy();
  });

  it("deletes the task itself when the global config also asks for it", async () => {
    vi.mocked(fetchPluginList).mockResolvedValue(pluginList({}, plugin({ project_schedules: { sync: { enabled: true } }, global_schedules: { sync: { enabled: true } } })));
    const { user } = await setup([task({ id: 4, plugin_id: "pl", plugin_action: "sync" })], { allowPlugins: true });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalled());
    await user.click(screen.getByTestId("task-row-4"));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteTask).toHaveBeenCalledWith(4);
    expect(setPluginScheduleEntry).not.toHaveBeenCalled();
  });
});

describe("TasksPanel create", () => {
  it("toggles the form and creates a prompt task", async () => {
    const { user } = await setup();
    const plus = screen.getByRole("button", { name: "+" });
    await user.click(plus);
    expect(screen.queryByText("No scheduled tasks")).toBeNull();
    expect(createBtn()).toHaveProperty("disabled", true);
    await user.type(screen.getByPlaceholderText("Task prompt..."), "hello");
    expect(createBtn()).toHaveProperty("disabled", false);
    await user.click(createBtn());
    expect(createTask).toHaveBeenCalledWith({
      channel_id: "ch",
      schedule: "*/30 * * * *",
      type: "cron",
      prompt: "hello",
      workflow_name: "",
      workflow_inputs: "",
      bash_script: "",
      worktree: false,
      origin_branch: undefined,
      update_before_run: false,
      auto_delete_sec: 0,
    });
    expect(await screen.findByText("No scheduled tasks")).toBeTruthy();
    expect(fetchTasks).toHaveBeenCalledTimes(2);

    await user.click(plus);
    expect(screen.getByPlaceholderText("Task prompt...")).toHaveProperty("value", "");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByPlaceholderText("Task prompt...")).toBeNull();
    await user.click(plus);
    await user.click(plus);
    expect(screen.queryByTestId("task-new-create")).toBeNull();
  });

  it("creates a manual bash task in a worktree", async () => {
    const { user } = await setup([], { allowWorktree: true });
    await user.click(screen.getByRole("button", { name: "+" }));
    await user.selectOptions(screen.getByDisplayValue("Cron"), "manual");
    expect(screen.queryByPlaceholderText("*/30 * * * *")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Bash" }));
    await user.type(screen.getByPlaceholderText(/Shell script/), "ls");
    // The submit gate reads the prompt even in bash mode.
    await user.click(screen.getByRole("button", { name: "Prompt" }));
    await user.type(screen.getByPlaceholderText("Task prompt..."), "p");
    await user.click(screen.getByRole("button", { name: "Bash" }));
    await user.click(screen.getByTestId("task-new-worktree"));
    await user.type(screen.getByPlaceholderText("Origin branch (auto-detect)"), "main");
    await user.click(screen.getByRole("checkbox", { name: "Update before run" }));
    const autoDel = screen.getByTestId("task-new-auto-delete");
    await user.clear(autoDel);
    await user.type(autoDel, "30");
    await user.click(createBtn());
    expect(createTask).toHaveBeenCalledWith(
      expect.objectContaining({ schedule: "", type: "manual", prompt: "", bash_script: "ls", worktree: true, origin_branch: "main", update_before_run: true, auto_delete_sec: 30 }),
    );
  });

  it("needs a time for a once task", async () => {
    const { user } = await setup();
    await user.click(screen.getByRole("button", { name: "+" }));
    await user.type(screen.getByPlaceholderText("Task prompt..."), "x");
    await user.selectOptions(screen.getByDisplayValue("Cron"), "once");
    expect(createBtn()).toHaveProperty("disabled", true);
    const picker = document.querySelector('input[type="datetime-local"]') as HTMLInputElement;
    fireEvent.change(picker, { target: { value: "2026-06-11T09:00" } });
    expect(createBtn()).toHaveProperty("disabled", false);
  });

  it("creates a workflow task with seeded inputs", async () => {
    const { user } = await setup();
    await user.click(screen.getByRole("button", { name: "+" }));
    await user.click(screen.getByRole("button", { name: "Workflow" }));
    expect(createBtn()).toHaveProperty("disabled", true);
    await user.selectOptions(screen.getByDisplayValue("Select workflow..."), "deploy");
    expect(screen.getByText("env * — where")).toBeTruthy();
    const env = screen.getByPlaceholderText("default: prod") as HTMLInputElement;
    expect(env.value).toBe("prod");
    const tag = screen.getByPlaceholderText("tag") as HTMLInputElement;
    await user.type(tag, "v1");
    await user.click(createBtn());
    expect(createTask).toHaveBeenCalledWith(expect.objectContaining({ prompt: "", workflow_name: "deploy", workflow_inputs: JSON.stringify({ env: "prod", tag: "v1" }), bash_script: "" }));
  });

  it("shows no inputs for a workflow without any, and an empty input a reloaded definition adds", async () => {
    const { user, rerender } = await setup();
    await user.click(screen.getByRole("button", { name: "+" }));
    await user.click(screen.getByRole("button", { name: "Workflow" }));
    await user.selectOptions(screen.getByDisplayValue("Select workflow..."), "plain");
    expect(screen.queryByPlaceholderText("tag")).toBeNull();
    // Another channel's definition of the same workflow has an input the
    // picked one didn't seed.
    vi.mocked(fetchWorkflows).mockResolvedValue([{ name: "plain", description: "", nodes: [], inputs: { extra: {} } }]);
    rerender(<TasksPanel channelId="ch2" />);
    const extra = (await screen.findByPlaceholderText("extra")) as HTMLInputElement;
    expect(extra.value).toBe("");
  });

  it("disables the workflow mode with no workflows", async () => {
    vi.mocked(fetchWorkflows).mockResolvedValue([]);
    const { user } = await setup();
    await user.click(screen.getByRole("button", { name: "+" }));
    const wf = screen.getByRole("button", { name: "Workflow" }) as HTMLButtonElement;
    expect(wf.disabled).toBe(true);
    expect(wf.title).toBe("No workflows defined");
  });

  it("ignores a failed create", async () => {
    vi.mocked(createTask).mockRejectedValue(new Error("x"));
    const { user } = await setup();
    await user.click(screen.getByRole("button", { name: "+" }));
    await user.type(screen.getByPlaceholderText("Task prompt..."), "hello");
    await user.click(createBtn());
    expect(createTask).toHaveBeenCalled();
    expect(screen.getByPlaceholderText("Task prompt...")).toHaveProperty("value", "hello");
  });
});

describe("TasksPanel create plugin task", () => {
  async function openPluginMode(list: PluginList = pluginList(), props: Partial<Props> = {}) {
    vi.mocked(fetchPluginList).mockResolvedValue(list);
    const view = await setup([], { allowPlugins: true, ...props });
    await view.user.click(screen.getByRole("button", { name: "+" }));
    await waitFor(() => expect(screen.getByTestId("task-new-plugin-mode")).toHaveProperty("disabled", false));
    return view;
  }

  it("disables the plugin mode with no schedulable action", async () => {
    vi.mocked(fetchPluginList).mockResolvedValue(pluginList({ off: true }));
    const { user } = await setup([], { allowPlugins: true });
    await user.click(screen.getByRole("button", { name: "+" }));
    const btn = screen.getByTestId("task-new-plugin-mode") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.title).toBe("No plugin enabled in this project has a schedulable action");
  });

  it("schedules a plugin action through the project config", async () => {
    const { user } = await openPluginMode();
    await user.selectOptions(screen.getByDisplayValue("Cron"), "once");
    await user.click(screen.getByTestId("task-new-plugin-mode"));
    // Plugin mode only takes cron and interval, and resets a once type.
    const typeSel = screen.getByDisplayValue("Cron") as HTMLSelectElement;
    expect(Array.from(typeSel.options).map((o) => o.value)).toEqual(["cron", "interval"]);
    expect(screen.getByText(/Sets plugins.<id>.schedules.<action>/)).toBeTruthy();
    expect(screen.queryByTestId("task-new-auto-delete")).toBeNull();
    expect(screen.queryByTestId("task-new-plugin-untrusted")).toBeNull();
    expect(createBtn()).toHaveProperty("disabled", true);

    await user.selectOptions(screen.getByTestId("task-new-plugin-action"), "pl/sync");
    expect(screen.getByText(/Sets plugins.pl.schedules.sync/)).toBeTruthy();
    expect(screen.getByPlaceholderText("*/30 * * * *")).toHaveProperty("value", "0 * * * *");
    expect(screen.queryByText(/already has a task here/)).toBeNull();
    expect(createBtn()).toHaveProperty("disabled", false);
    await user.clear(screen.getByPlaceholderText("*/30 * * * *"));
    expect(createBtn()).toHaveProperty("disabled", true);
    await user.type(screen.getByPlaceholderText("*/30 * * * *"), "5 * * * *");

    await user.click(createBtn());
    expect(setPluginScheduleEntry).toHaveBeenCalledWith("pl", "sync", { enabled: true, type: undefined, schedule: "5 * * * *" }, "ch");
    // No worktree: the task's run options aren't touched.
    expect(updateTask).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByTestId("task-new-create")).toBeNull());
    expect(createTask).not.toHaveBeenCalled();
  });

  it("keeps an interval type and clears the picked action", async () => {
    const { user } = await openPluginMode();
    await user.selectOptions(screen.getByDisplayValue("Cron"), "interval");
    await user.click(screen.getByTestId("task-new-plugin-mode"));
    expect(screen.getByTestId("task-interval-value")).toHaveProperty("value", "30");
    await user.selectOptions(screen.getByTestId("task-new-plugin-action"), "pl/sync");
    await user.selectOptions(screen.getByTestId("task-new-plugin-action"), "");
    expect(createBtn()).toHaveProperty("disabled", true);
    // The schedule stays the one the last action set.
    expect(screen.getByPlaceholderText("*/30 * * * *")).toHaveProperty("value", "0 * * * *");
  });

  it("sets the made task's worktree options", async () => {
    vi.mocked(fetchTasks).mockResolvedValue([]);
    const { user } = await openPluginMode(pluginList(), { allowWorktree: true });
    await user.click(screen.getByTestId("task-new-plugin-mode"));
    await user.selectOptions(screen.getByTestId("task-new-plugin-action"), "pl/sync");
    await user.click(screen.getByTestId("task-new-worktree"));
    expect(screen.getByTestId("task-new-auto-delete")).toBeTruthy();
    await user.type(screen.getByPlaceholderText("Origin branch (auto-detect)"), " dev ");
    vi.mocked(fetchTasks).mockResolvedValue([task({ id: 1, plugin_id: "other", plugin_action: "sync" }), task({ id: 9, plugin_id: "pl", plugin_action: "sync" })]);
    await user.click(createBtn());
    await waitFor(() => expect(updateTask).toHaveBeenCalledWith(9, { worktree: true, origin_branch: "dev", update_before_run: false, auto_delete_sec: 0 }));
  });

  it("skips the run options when the task isn't made yet", async () => {
    const { user } = await openPluginMode(pluginList(), { allowWorktree: true });
    await user.click(screen.getByTestId("task-new-plugin-mode"));
    await user.selectOptions(screen.getByTestId("task-new-plugin-action"), "pl/sync");
    await user.click(screen.getByTestId("task-new-worktree"));
    await user.click(createBtn());
    await waitFor(() => expect(screen.queryByTestId("task-new-create")).toBeNull());
    expect(updateTask).not.toHaveBeenCalled();
  });

  it("shows the config's notes and a failed write", async () => {
    const p = plugin({ schedules: { sync: { enabled: true } }, schedule_problems: ["bad cron", "worse"] });
    vi.mocked(setPluginScheduleEntry).mockRejectedValueOnce(new Error("locked")).mockRejectedValueOnce("plain");
    const { user } = await openPluginMode(pluginList({ projectTrusted: false }, p));
    await user.click(screen.getByTestId("task-new-plugin-mode"));
    expect(screen.getByTestId("task-new-plugin-untrusted")).toBeTruthy();
    await user.selectOptions(screen.getByTestId("task-new-plugin-action"), "pl/sync");
    expect(screen.getByText(/already has a task here/)).toBeTruthy();
    expect(screen.getByText("No task for: bad cron; worse")).toBeTruthy();
    await user.click(createBtn());
    expect(await screen.findByText("locked")).toBeTruthy();
    await user.click(createBtn());
    expect(await screen.findByText("plain")).toBeTruthy();
  });
});

describe("TasksPanel edit", () => {
  async function openEdit(t: ScheduledTask, props: Partial<Props> = {}) {
    const view = await setup([t], props);
    await view.user.click(screen.getByTestId(`task-row-${t.id}`));
    await view.user.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByText(`Edit Task #${t.id}`)).toBeTruthy();
    return view;
  }

  it("edits a prompt task's schedule and prompt", async () => {
    const { user } = await openEdit(task({ id: 2, prompt: "old" }));
    const prompt = screen.getByDisplayValue("old");
    await user.clear(prompt);
    await user.type(prompt, "new");
    await user.selectOptions(screen.getByTestId("task-edit-type"), "interval");
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalledWith(2, {
      schedule: "30m",
      type: "interval",
      prompt: "new",
      workflow_name: "",
      workflow_inputs: "",
      bash_script: "",
      worktree: false,
      origin_branch: undefined,
      update_before_run: false,
      auto_delete_sec: 0,
    });
    expect(await screen.findByText("Task #2")).toBeTruthy();
    expect(fetchTasks).toHaveBeenCalledTimes(2);
  });

  it("edits a bash task into a manual worktree task", async () => {
    const { user } = await openEdit(task({ id: 2, bash_script: "ls", worktree: true, origin_branch: "main", update_before_run: true, auto_delete_sec: 5 }), { allowWorktree: true });
    expect(screen.getByTestId("task-edit-worktree")).toHaveProperty("checked", true);
    await user.click(screen.getByTestId("task-edit-worktree"));
    expect(screen.queryByPlaceholderText("Origin branch (auto-detect)")).toBeNull();
    await user.click(screen.getByTestId("task-edit-worktree"));
    const script = screen.getByDisplayValue("ls");
    await user.type(script, " -la");
    await user.selectOptions(screen.getByTestId("task-edit-type"), "manual");
    const branch = screen.getByDisplayValue("main");
    await user.type(branch, "2");
    await user.click(screen.getByRole("checkbox", { name: "Update before run" }));
    const autoDel = screen.getByTestId("task-edit-auto-delete");
    await user.clear(autoDel);
    await user.type(autoDel, "9");
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalledWith(
      2,
      expect.objectContaining({ schedule: "", type: "manual", prompt: "", bash_script: "ls -la", worktree: true, origin_branch: "main2", update_before_run: false, auto_delete_sec: 9 }),
    );
  });

  it("edits a workflow task's inputs", async () => {
    const { user } = await openEdit(task({ id: 2, workflow_name: "deploy", workflow_inputs: '{"env":"qa"}' }));
    expect(screen.getByDisplayValue("qa")).toBeTruthy();
    // A key the saved inputs lack shows empty.
    expect(screen.getByPlaceholderText("tag")).toHaveProperty("value", "");
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenLastCalledWith(2, expect.objectContaining({ workflow_inputs: JSON.stringify({ env: "qa" }) }));
    // Picking a workflow keeps the inputs already set.
    vi.mocked(fetchTasks).mockResolvedValue([task({ id: 2, workflow_name: "plain", workflow_inputs: '{"env":"qa"}' })]);
    emit("task.updated");
    expect(await screen.findByText("workflow: plain")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.selectOptions(screen.getByDisplayValue("plain"), "deploy");
    expect(screen.getByPlaceholderText("default: prod")).toHaveProperty("value", "qa");
    await user.type(screen.getByPlaceholderText("tag"), "v2");
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalledWith(2, expect.objectContaining({ prompt: "", workflow_name: "deploy", workflow_inputs: JSON.stringify({ env: "qa", tag: "v2" }), bash_script: "" }));
  });

  it("seeds defaults when switching to a workflow, and survives bad saved inputs", async () => {
    const { user } = await openEdit(task({ id: 2, workflow_name: "plain", workflow_inputs: "not json" }));
    await user.selectOptions(screen.getByDisplayValue("plain"), "deploy");
    expect(screen.getByPlaceholderText("default: prod")).toHaveProperty("value", "prod");
    expect(screen.getByPlaceholderText("tag")).toHaveProperty("value", "");
    await user.selectOptions(screen.getByDisplayValue("deploy"), "");
    expect(screen.queryByPlaceholderText("tag")).toBeNull();
    await user.selectOptions(screen.getByDisplayValue("Select workflow..."), "deploy");
    await user.click(screen.getByRole("button", { name: "Prompt" }));
    expect(screen.queryByPlaceholderText("tag")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Bash" }));
    await user.click(screen.getByRole("button", { name: "Workflow" }));
    expect(screen.getByPlaceholderText("tag")).toBeTruthy();
  });

  it("disables the workflow mode with no workflows and cancels", async () => {
    vi.mocked(fetchWorkflows).mockResolvedValue([]);
    const { user } = await openEdit(task({ id: 2 }));
    const wf = screen.getByRole("button", { name: "Workflow" }) as HTMLButtonElement;
    expect(wf.disabled).toBe(true);
    expect(wf.title).toBe("No workflows defined");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("Task #2")).toBeTruthy();
  });

  it("stays in the form when the save fails", async () => {
    vi.mocked(updateTask).mockRejectedValue(new Error("x"));
    const { user } = await openEdit(task({ id: 2 }));
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalled();
    expect(screen.getByText("Edit Task #2")).toBeTruthy();
  });

  it("leaves the edit form when another row is picked", async () => {
    const { user } = await openEdit(task({ id: 2 }));
    await user.click(screen.getByTestId("task-row-2"));
    expect(screen.getByText("Task #2")).toBeTruthy();
  });
});

describe("TasksPanel edit plugin task", () => {
  const pt = (o: Partial<ScheduledTask> = {}) => task({ id: 6, plugin_id: "pl", plugin_action: "sync", schedule: "0 * * * *", ...o });

  async function openEdit(t: ScheduledTask, props: Partial<Props> = {}) {
    const view = await setup([t], { allowPlugins: true, ...props });
    await waitFor(() => expect(fetchPluginList).toHaveBeenCalled());
    await view.user.click(screen.getByTestId(`task-row-${t.id}`));
    await view.user.click(screen.getByRole("button", { name: "Edit" }));
    return view;
  }

  it("saves the run options on the task and the schedule in the config", async () => {
    const { user } = await openEdit(pt({ worktree: true, origin_branch: "main" }), { allowWorktree: true });
    const typeSel = screen.getByTestId("task-edit-type") as HTMLSelectElement;
    expect(Array.from(typeSel.options).map((o) => o.value)).toEqual(["cron", "interval"]);
    expect(screen.getByTestId("task-edit-plugin").textContent).toContain("Saves the schedule in plugins.pl.schedules.sync");
    expect(screen.queryByRole("button", { name: "Prompt" })).toBeNull();
    expect(screen.getByTestId("task-edit-auto-delete")).toBeTruthy();
    await user.selectOptions(typeSel, "interval");
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalledWith(6, { worktree: true, origin_branch: "main", update_before_run: false, auto_delete_sec: 0 });
    await waitFor(() => expect(setPluginScheduleEntry).toHaveBeenCalledWith("pl", "sync", { enabled: true, type: "interval", schedule: "30m" }, "ch"));
    expect(await screen.findByText("Task #6")).toBeTruthy();
  });

  it("clears the run options without a worktree and shows a failed task update", async () => {
    vi.mocked(updateTask).mockRejectedValueOnce(new Error("bad opts")).mockRejectedValueOnce("raw");
    const { user } = await openEdit(pt());
    expect(screen.queryByTestId("task-edit-auto-delete")).toBeNull();
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalledWith(6, { worktree: false, origin_branch: "", update_before_run: false, auto_delete_sec: 0 });
    expect(await screen.findByText("bad opts")).toBeTruthy();
    await user.click(screen.getByTestId("task-edit-save"));
    expect(await screen.findByText("raw")).toBeTruthy();
    expect(setPluginScheduleEntry).not.toHaveBeenCalled();
  });

  it("stays in the form when the config write fails", async () => {
    vi.mocked(setPluginScheduleEntry).mockRejectedValue(new Error("locked"));
    const { user } = await openEdit(pt());
    await user.click(screen.getByTestId("task-edit-save"));
    expect(await screen.findByText("locked")).toBeTruthy();
    expect(screen.getByText("Edit Task #6")).toBeTruthy();
  });

  it("updates only the task when its plugin isn't on here", async () => {
    const { user } = await openEdit(pt({ plugin_id: "gone" }));
    expect(screen.getByTestId("task-edit-plugin").textContent).toContain("The plugin isn't on in this project");
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalledWith(6, { schedule: "0 * * * *", type: "cron", worktree: false, origin_branch: "", update_before_run: false, auto_delete_sec: 0 });
    expect(await screen.findByText("Task #6")).toBeTruthy();
    expect(fetchTasks).toHaveBeenCalledTimes(2);
    expect(setPluginScheduleEntry).not.toHaveBeenCalled();
  });

  it("stays in the form when updating a task whose plugin is off fails", async () => {
    vi.mocked(updateTask).mockRejectedValue(new Error("x"));
    const { user } = await openEdit(pt({ plugin_id: "gone" }));
    await user.click(screen.getByTestId("task-edit-save"));
    expect(updateTask).toHaveBeenCalled();
    expect(screen.getByText("Edit Task #6")).toBeTruthy();
  });
});
