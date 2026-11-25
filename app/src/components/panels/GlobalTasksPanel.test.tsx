import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deleteTask, fetchAllTasks, fetchTaskRuns, fetchWorkflows, runTaskNow, type ScheduledTask, type TaskRunLog, updateTask, type WorkflowDef } from "../../api/loopApi";
import type { Channel } from "../../types";
import { GlobalTasksPanel } from "./GlobalTasksPanel";

vi.mock("../../api/loopApi", () => ({
  deleteTask: vi.fn(),
  fetchAllTasks: vi.fn(),
  fetchTaskRuns: vi.fn(),
  fetchWorkflows: vi.fn(),
  runTaskNow: vi.fn(),
  updateTask: vi.fn(),
}));

// The header info fetches PR and commit data of its own; the stub just shows
// which channel the panel handed it.
vi.mock("../layout/ChannelHeaderInfo", () => ({
  ChannelHeaderInfo: ({ channel }: { channel: Channel }) => <div data-testid="header-info">{channel.id}</div>,
}));

function task(over: Partial<ScheduledTask> = {}): ScheduledTask {
  return {
    id: 1,
    channel_id: "chan-abcdef-123",
    schedule: "*/5 * * * *",
    type: "cron",
    prompt: "do the thing",
    enabled: true,
    next_run_at: new Date(Date.now() + 30 * 60_000).toISOString(),
    auto_delete_sec: 0,
    worktree: false,
    update_before_run: false,
    running: false,
    ...over,
  };
}

function runLog(over: Partial<TaskRunLog> = {}): TaskRunLog {
  return {
    id: 1,
    task_id: 1,
    status: "success",
    response_text: "",
    error_text: "",
    started_at: new Date().toISOString(),
    finished_at: "",
    ...over,
  };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const wf: WorkflowDef = {
  name: "deploy",
  description: "",
  nodes: [],
  inputs: { env: { description: "target env", default: "prod" }, tag: {}, region: { default: "eu" } },
};

function setup(tasks: ScheduledTask[], props: Partial<Parameters<typeof GlobalTasksPanel>[0]> = {}) {
  vi.mocked(fetchAllTasks).mockResolvedValue(tasks);
  const onClose = vi.fn();
  const onSelectChannel = vi.fn();
  const user = userEvent.setup();
  render(<GlobalTasksPanel onClose={onClose} onSelectChannel={onSelectChannel} {...props} />);
  return { onClose, onSelectChannel, user };
}

async function selectRow(user: ReturnType<typeof userEvent.setup>, id: number) {
  await user.click(await screen.findByTestId(`task-row-${id}`));
}

beforeEach(() => {
  vi.mocked(fetchTaskRuns).mockResolvedValue([]);
  vi.mocked(fetchWorkflows).mockResolvedValue([]);
  vi.mocked(updateTask).mockResolvedValue(undefined as never);
  vi.mocked(deleteTask).mockResolvedValue(undefined as never);
  vi.mocked(runTaskNow).mockResolvedValue(undefined as never);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
  vi.useRealTimers();
});

describe("GlobalTasksPanel list", () => {
  it("shows the empty state and keeps an empty list when loading fails", async () => {
    vi.mocked(fetchAllTasks).mockRejectedValue(new Error("down"));
    render(<GlobalTasksPanel onClose={vi.fn()} />);
    expect(await screen.findByText("No scheduled tasks")).toBeTruthy();
    expect(screen.getByText("Tasks (0)")).toBeTruthy();
    expect(screen.getByText("Select a task to view details")).toBeTruthy();
    await waitFor(() => expect(fetchAllTasks).toHaveBeenCalled());
  });

  it("polls tasks every ten seconds and stops on unmount", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchAllTasks).mockResolvedValue([]);
    const { unmount } = render(<GlobalTasksPanel onClose={vi.fn()} />);
    expect(fetchAllTasks).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(fetchAllTasks).toHaveBeenCalledTimes(2);
    unmount();
    await vi.advanceTimersByTimeAsync(20_000);
    expect(fetchAllTasks).toHaveBeenCalledTimes(2);
  });

  it("renders rows with enabled tasks first and their badges", async () => {
    const tasks = [
      task({ id: 1, enabled: false, type: "manual", schedule: "", channel_name: "alpha" }),
      task({ id: 2, worktree: true, channel_worktree: true, dir_path: "/repo/two", workflow_name: "deploy" }),
      task({ id: 3, type: "weird" as ScheduledTask["type"], enabled: false }),
      task({ id: 4, type: "manual", schedule: "" }),
    ];
    const { user } = setup(tasks);
    const row2 = await screen.findByTestId("task-row-2");
    const rows = screen.getAllByTestId(/^task-row-/).map((r) => r.dataset.testid);
    expect(rows).toEqual(["task-row-2", "task-row-4", "task-row-1", "task-row-3"]);
    expect(screen.getByText("Tasks (4)")).toBeTruthy();

    const r2 = within(row2);
    expect(r2.getByTitle("Runs in worktree")).toBeTruthy();
    expect(r2.getByTitle("Channel is a worktree")).toBeTruthy();
    expect(r2.getByText("/repo/two")).toBeTruthy();
    expect(r2.getByText("workflow: deploy")).toBeTruthy();
    expect(r2.getByText("#chan-abc")).toBeTruthy();
    expect(r2.getByText(/^Next: in \d+m$/)).toBeTruthy();
    expect(r2.getByTitle("Disable").textContent).toBe("●");

    const r1 = within(screen.getByTestId("task-row-1"));
    expect(r1.getByText("on demand")).toBeTruthy();
    expect(r1.getByText("#alpha")).toBeTruthy();
    expect(r1.getByTitle("Enable").textContent).toBe("○");
    expect(r1.queryByText(/^Next:/)).toBeNull();
    expect(screen.getByTestId("task-row-1").style.opacity).toBe("0.5");

    // An enabled manual task has no next run either.
    expect(within(screen.getByTestId("task-row-4")).queryByText(/^Next:/)).toBeNull();
    // Unknown types fall back to the dim text color.
    expect(within(screen.getByTestId("task-row-3")).getByText("weird").style.background).not.toBe("");
    await selectRow(user, 3);
    expect(screen.getByText("Task #3")).toBeTruthy();
    expect(screen.getAllByText("weird")[1]!.style.background).toBe(screen.getAllByText("weird")[0]!.style.background);
  });

  it("highlights the hovered row but not the selected one", async () => {
    const { user } = setup([task({ id: 1 }), task({ id: 2 })]);
    const row1 = await screen.findByTestId("task-row-1");
    const row2 = screen.getByTestId("task-row-2");
    fireEvent.mouseEnter(row1);
    expect(row1.style.background).toBe("rgba(255, 255, 255, 0.04)");
    fireEvent.mouseLeave(row1);
    expect(row1.style.background).toBe("transparent");

    await user.click(row2);
    const selectedBg = row2.style.background;
    fireEvent.mouseEnter(row2);
    expect(row2.style.background).toBe(selectedBg);
    fireEvent.mouseLeave(row2);
    expect(row2.style.background).toBe(selectedBg);
  });

  it("toggles a task from its row without selecting it and reloads", async () => {
    const t = task({ id: 7 });
    const { user } = setup([t]);
    await user.click(await screen.findByTitle("Disable"));
    expect(updateTask).toHaveBeenCalledWith(7, { enabled: false });
    await waitFor(() => expect(fetchAllTasks).toHaveBeenCalledTimes(2));
    expect(screen.getByText("Select a task to view details")).toBeTruthy();
  });

  it("ignores a failed toggle", async () => {
    vi.mocked(updateTask).mockRejectedValue(new Error("nope"));
    const { user } = setup([task({ id: 7, enabled: false })]);
    await user.click(await screen.findByTitle("Enable"));
    expect(updateTask).toHaveBeenCalledWith(7, { enabled: true });
    expect(fetchAllTasks).toHaveBeenCalledTimes(1);
  });

  it("jumps to the task's channel from the row tag", async () => {
    const { user, onSelectChannel } = setup([task({ id: 1 })]);
    await user.click(await screen.findByTitle("Go to channel"));
    expect(onSelectChannel).toHaveBeenCalledWith("chan-abcdef-123");
    expect(screen.getByText("Select a task to view details")).toBeTruthy();
    expect(screen.getByTitle("Go to channel").style.cursor).toBe("pointer");
  });

  it("does nothing on the channel tags without a channel handler", async () => {
    vi.mocked(fetchAllTasks).mockResolvedValue([task({ id: 1 })]);
    const user = userEvent.setup();
    render(<GlobalTasksPanel onClose={vi.fn()} />);
    const tag = await screen.findByTitle("Go to channel");
    expect(tag.style.cursor).toBe("default");
    await user.click(tag);
    await selectRow(user, 1);
    const detailTag = screen.getAllByText("#chan-abc")[1]!;
    expect(detailTag.style.cursor).toBe("default");
    await user.click(detailTag);
    expect(screen.getByText("Task #1")).toBeTruthy();
  });

  it("resizes the list within its bounds while dragging the divider", async () => {
    setup([task({ id: 1 })]);
    const row = await screen.findByTestId("task-row-1");
    const list = row.parentElement!.parentElement!;
    const divider = list.nextElementSibling!;
    expect(list.style.width).toBe("380px");

    fireEvent.mouseDown(divider);
    fireEvent.mouseMove(window, { movementX: 50 });
    expect(list.style.width).toBe("430px");
    fireEvent.mouseMove(window, { movementX: 1000 });
    expect(list.style.width).toBe("600px");
    fireEvent.mouseMove(window, { movementX: -1000 });
    expect(list.style.width).toBe("240px");
    fireEvent.mouseUp(window);
    fireEvent.mouseMove(window, { movementX: 100 });
    expect(list.style.width).toBe("240px");
  });
});

describe("GlobalTasksPanel header", () => {
  it("closes on Escape and the close button, ignoring other keys", async () => {
    const { user, onClose } = setup([]);
    fireEvent.keyDown(window, { key: "a" });
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);

    const close = screen.getByTitle("Close panel");
    fireEvent.mouseEnter(close);
    expect(close.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(close);
    expect(close.style.backgroundColor).toBe("transparent");
    await user.click(close);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("shows the palette button with the Mac shortcut and channel info", async () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    const onOpenPalette = vi.fn();
    const { user } = setup([], { onOpenPalette, channel: { id: "c-1" } as Channel, sidebarOpen: false });
    const btn = screen.getByTitle("Search messages (Cmd+K)");
    expect(btn.textContent).toContain("⌘K");
    expect((btn.parentElement as HTMLElement).style.paddingLeft).toBe("76px");
    expect(screen.getByTestId("header-info").textContent).toBe("c-1");
    await user.click(btn);
    expect(onOpenPalette).toHaveBeenCalledTimes(1);
  });

  it("shows the Ctrl shortcut off Mac and leaves out optional header parts", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    setup([], { onOpenPalette: vi.fn() });
    const btn = screen.getByTitle("Search messages (Cmd+K)");
    expect(btn.textContent).toContain("Ctrl+K");
    expect((btn.parentElement as HTMLElement).style.paddingLeft).toBe("4px");
    expect(screen.queryByTestId("header-info")).toBeNull();
  });

  it("has no palette button without a handler", () => {
    setup([]);
    expect(screen.queryByTitle("Search messages (Cmd+K)")).toBeNull();
  });
});

describe("GlobalTasksPanel detail", () => {
  it("shows a prompt task with its schedule, branch, auto-delete and run history", async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([
      runLog({ id: 1, status: "success", response_text: "x".repeat(300) }),
      runLog({ id: 2, status: "failed", error_text: "boom" }),
      runLog({ id: 3, status: "running" }),
    ]);
    const t = task({
      id: 5,
      worktree: true,
      origin_branch: "main",
      update_before_run: true,
      auto_delete_sec: 60,
      dir_path: "/repo",
      channel_name: "proj",
      channel_worktree: true,
    });
    const { user, onSelectChannel } = setup([t]);
    await selectRow(user, 5);
    expect(screen.getByText("Task #5")).toBeTruthy();
    expect(fetchTaskRuns).toHaveBeenCalledWith(5);
    expect(fetchWorkflows).toHaveBeenCalledWith("chan-abcdef-123");
    expect(screen.getByText("worktree")).toBeTruthy();
    expect(screen.getByText("channel worktree")).toBeTruthy();
    expect(screen.getByText("Branch: main (updates before run)")).toBeTruthy();
    expect(screen.getByText("Auto-delete after 60s")).toBeTruthy();
    expect(screen.getAllByText("/repo")).toHaveLength(2);
    expect(screen.getByText("do the thing", { selector: "div" })).toBeTruthy();
    expect(screen.getByText(/^Next run: in \d+m$/)).toBeTruthy();
    expect(screen.getAllByText("*/5 * * * *")).toHaveLength(2);

    expect(await screen.findByText("x".repeat(200))).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
    expect(screen.getByText("failed")).toBeTruthy();
    expect(screen.getByText("running")).toBeTruthy();

    await user.click(screen.getAllByText("#proj")[1]!);
    expect(onSelectChannel).toHaveBeenCalledWith("chan-abcdef-123");
  });

  it("shows a manual disabled task with no runs and a branch that does not update", async () => {
    const t = task({ id: 6, type: "manual", enabled: false, worktree: true, origin_branch: "dev" });
    const { user } = setup([t]);
    await selectRow(user, 6);
    expect(screen.getByText("on demand (run manually)")).toBeTruthy();
    expect(screen.getByText("Branch: dev")).toBeTruthy();
    expect(screen.queryByText(/^Next run:/)).toBeNull();
    expect(screen.queryByText(/Auto-delete after/)).toBeNull();
    expect(await screen.findByText("No runs yet")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Enable" })).toBeTruthy();
  });

  it("shows an enabled manual task without a next run and a worktree with no branch", async () => {
    const { user } = setup([task({ id: 6, type: "manual", worktree: true })]);
    await selectRow(user, 6);
    expect(screen.queryByText(/^Next run:/)).toBeNull();
    expect(screen.queryByText(/^Branch:/)).toBeNull();
  });

  it("describes plugin, workflow and bash tasks", async () => {
    const tasks = [
      task({ id: 1, plugin_id: "acme", plugin_action: "sync" }),
      task({ id: 2, workflow_name: "deploy", workflow_inputs: '{"env":"prod"}' }),
      task({ id: 3, workflow_name: "deploy", workflow_inputs: "{}" }),
      task({ id: 4, workflow_name: "deploy" }),
      task({ id: 5, bash_script: "echo hi" }),
    ];
    const { user } = setup(tasks);
    await selectRow(user, 1);
    expect(screen.getByTestId("task-detail-plugin").textContent).toBe("Plugin: acme's sync action");

    await selectRow(user, 2);
    expect(screen.getByText("Workflow:")).toBeTruthy();
    expect(screen.getByText('Inputs: {"env":"prod"}')).toBeTruthy();

    await selectRow(user, 3);
    expect(screen.getByText("Workflow:")).toBeTruthy();
    expect(screen.queryByText(/^Inputs:/)).toBeNull();

    await selectRow(user, 4);
    expect(screen.queryByText(/^Inputs:/)).toBeNull();

    await selectRow(user, 5);
    expect(screen.getByText("Bash:")).toBeTruthy();
    expect(screen.getByText("echo hi", { selector: "pre" })).toBeTruthy();
  });

  it("ignores failures loading runs and workflows", async () => {
    vi.mocked(fetchTaskRuns).mockRejectedValue(new Error("x"));
    vi.mocked(fetchWorkflows).mockRejectedValue(new Error("y"));
    const { user } = setup([task({ id: 1 })]);
    await selectRow(user, 1);
    await waitFor(() => expect(fetchWorkflows).toHaveBeenCalled());
    expect(screen.getByText("No runs yet")).toBeTruthy();
  });

  it("runs a task now, marks it running and reloads its runs", async () => {
    const d = deferred<void>();
    vi.mocked(runTaskNow).mockReturnValue(d.promise as never);
    const { user } = setup([task({ id: 1 }), task({ id: 2 })]);
    await selectRow(user, 1);
    await user.click(screen.getByRole("button", { name: "▶ Run Now" }));
    const starting = screen.getByRole("button", { name: "Starting..." }) as HTMLButtonElement;
    expect(starting.disabled).toBe(true);
    expect(runTaskNow).toHaveBeenCalledWith(1);
    await act(async () => d.resolve());
    const running = await screen.findByRole("button", { name: "Running..." });
    expect((running as HTMLButtonElement).disabled).toBe(true);
    expect(fetchTaskRuns).toHaveBeenCalledTimes(2);

    // The other task stays idle.
    await selectRow(user, 2);
    expect(screen.getByRole("button", { name: "▶ Run Now" })).toBeTruthy();
  });

  it("recovers the run button when starting a run fails", async () => {
    vi.mocked(runTaskNow).mockRejectedValue(new Error("x"));
    const { user } = setup([task({ id: 1 })]);
    await selectRow(user, 1);
    await user.click(screen.getByRole("button", { name: "▶ Run Now" }));
    const btn = (await screen.findByRole("button", { name: "▶ Run Now" })) as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(fetchTaskRuns).toHaveBeenCalledTimes(1);
  });

  it("opens the running thread when the task has one", async () => {
    const { user, onSelectChannel } = setup([task({ id: 1, running: true, thread_id: "thr-9" })]);
    await selectRow(user, 1);
    await user.click(screen.getByRole("button", { name: "Running..." }));
    expect(onSelectChannel).toHaveBeenCalledWith("thr-9");
  });

  it("does nothing on the running button without a channel handler", async () => {
    vi.mocked(fetchAllTasks).mockResolvedValue([task({ id: 1, running: true, thread_id: "thr-9" })]);
    const user = userEvent.setup();
    render(<GlobalTasksPanel onClose={vi.fn()} />);
    await selectRow(user, 1);
    await user.click(screen.getByRole("button", { name: "Running..." }));
    expect(screen.getByText("Task #1")).toBeTruthy();
  });

  it("toggles the selected task from the detail view", async () => {
    const { user } = setup([task({ id: 3 })]);
    await selectRow(user, 3);
    await user.click(screen.getByRole("button", { name: "Disable" }));
    expect(updateTask).toHaveBeenCalledWith(3, { enabled: false });
  });

  it("deletes the selected task and clears the selection", async () => {
    const { user } = setup([task({ id: 3 })]);
    await selectRow(user, 3);
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteTask).toHaveBeenCalledWith(3);
    expect(await screen.findByText("Select a task to view details")).toBeTruthy();
    expect(fetchAllTasks).toHaveBeenCalledTimes(2);
  });

  it("keeps the selection when deleting fails", async () => {
    vi.mocked(deleteTask).mockRejectedValue(new Error("x"));
    const { user } = setup([task({ id: 3 })]);
    await selectRow(user, 3);
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(deleteTask).toHaveBeenCalledWith(3);
    expect(screen.getByText("Task #3")).toBeTruthy();
    expect(fetchAllTasks).toHaveBeenCalledTimes(1);
  });
});

describe("GlobalTasksPanel edit", () => {
  it("edits a prompt task and saves every field", async () => {
    const t = task({ id: 4, prompt: "old" });
    const { user } = setup([t]);
    await selectRow(user, 4);
    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByText("Edit Task #4")).toBeTruthy();
    // No workflows loaded for this channel, so the workflow mode is off.
    const wfBtn = screen.getByRole("button", { name: "Workflow" }) as HTMLButtonElement;
    expect(wfBtn.disabled).toBe(true);
    expect(wfBtn.title).toBe("No workflows defined");

    const prompt = screen.getByDisplayValue("old");
    await user.clear(prompt);
    await user.type(prompt, "new prompt");
    const sched = screen.getByPlaceholderText("*/30 * * * *");
    await user.clear(sched);
    await user.type(sched, "0 * * * *");
    await user.click(screen.getByRole("checkbox", { name: "Worktree" }));
    await user.type(screen.getByPlaceholderText("Origin branch (auto-detect)"), "main");
    await user.click(screen.getByRole("checkbox", { name: "Update before run" }));
    const auto = screen.getByRole("spinbutton");
    await user.clear(auto);
    await user.type(auto, "30");

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(updateTask).toHaveBeenCalledWith(4, {
      schedule: "0 * * * *",
      type: "cron",
      prompt: "new prompt",
      workflow_name: "",
      workflow_inputs: "",
      bash_script: "",
      worktree: true,
      origin_branch: "main",
      update_before_run: true,
      auto_delete_sec: 30,
    });
    expect(await screen.findByText("Task #4")).toBeTruthy();
    expect(fetchAllTasks).toHaveBeenCalledTimes(2);
  });

  it("switches a task to manual bash and saves no schedule or branch", async () => {
    const { user } = setup([task({ id: 4, channel_worktree: true })]);
    await selectRow(user, 4);
    await user.click(screen.getByRole("button", { name: "Edit" }));
    // A worktree channel already isolates the run, so there is no checkbox.
    expect(screen.queryByRole("checkbox", { name: "Worktree" })).toBeNull();
    await user.selectOptions(screen.getByRole("combobox"), "manual");
    expect(screen.queryByPlaceholderText("*/30 * * * *")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Bash" }));
    const textareas = screen.getAllByRole("textbox");
    await user.type(textareas[textareas.length - 1]!, "ls");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(updateTask).toHaveBeenCalledWith(4, expect.objectContaining({ schedule: "", type: "manual", prompt: "", bash_script: "ls", workflow_name: "", origin_branch: undefined }));
  });

  it("swaps in the default schedule when the type changes", async () => {
    const { user } = setup([task({ id: 4 })]);
    await selectRow(user, 4);
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.selectOptions(screen.getByRole("combobox"), "interval");
    expect(screen.getByTestId("task-interval-value")).toBeTruthy();
  });

  it("opens a bash task in bash mode and prompt mode can be chosen back", async () => {
    const { user } = setup([task({ id: 4, bash_script: "echo a", origin_branch: "dev", worktree: true })]);
    await selectRow(user, 4);
    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByDisplayValue("echo a")).toBeTruthy();
    expect(screen.getByDisplayValue("dev")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Prompt" }));
    expect(screen.getByDisplayValue("do the thing")).toBeTruthy();
  });

  it("edits workflow inputs, seeding them from the definition", async () => {
    vi.mocked(fetchWorkflows).mockResolvedValue([wf, { name: "plain", description: "", nodes: [] }]);
    const t = task({ id: 8, workflow_name: "deploy", workflow_inputs: '{"tag":"v1"}' });
    const { user } = setup([t]);
    await selectRow(user, 8);
    await waitFor(() => expect(fetchWorkflows).toHaveBeenCalledWith("chan-abcdef-123"));
    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByDisplayValue("v1")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Workflow" }) as HTMLButtonElement).title).toBe("");
    expect(screen.getByPlaceholderText("env — target env")).toBeTruthy();
    expect(screen.getByPlaceholderText("tag")).toBeTruthy();

    // Picking a workflow keeps typed values and fills the rest from defaults.
    const picker = screen.getAllByRole("combobox")[1]!;
    await user.selectOptions(picker, "plain");
    expect(screen.queryByPlaceholderText("tag")).toBeNull();
    await user.selectOptions(picker, "deploy");
    expect((screen.getByPlaceholderText("env — target env") as HTMLInputElement).value).toBe("prod");
    expect((screen.getByPlaceholderText("tag") as HTMLInputElement).value).toBe("");
    expect((screen.getByPlaceholderText("region") as HTMLInputElement).value).toBe("eu");
    await user.type(screen.getByPlaceholderText("tag"), "v2");
    await user.selectOptions(picker, "deploy");
    expect((screen.getByPlaceholderText("tag") as HTMLInputElement).value).toBe("v2");

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(updateTask).toHaveBeenCalledWith(
      8,
      expect.objectContaining({
        workflow_name: "deploy",
        workflow_inputs: JSON.stringify({ env: "prod", tag: "v2", region: "eu" }),
        prompt: "",
        bash_script: "",
      }),
    );
  });

  it("starts with empty inputs when the stored ones are not valid JSON", async () => {
    vi.mocked(fetchWorkflows).mockResolvedValue([wf]);
    const { user } = setup([task({ id: 8, workflow_name: "deploy", workflow_inputs: "{bad" })]);
    await selectRow(user, 8);
    await waitFor(() => expect(fetchWorkflows).toHaveBeenCalled());
    await user.click(screen.getByRole("button", { name: "Edit" }));
    expect((screen.getByPlaceholderText("tag") as HTMLInputElement).value).toBe("");
  });

  it("switches a prompt task into workflow mode", async () => {
    vi.mocked(fetchWorkflows).mockResolvedValue([wf]);
    const { user } = setup([task({ id: 8 })]);
    await selectRow(user, 8);
    await waitFor(() => expect(fetchWorkflows).toHaveBeenCalled());
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.click(screen.getByRole("button", { name: "Workflow" }));
    expect(screen.getByRole("option", { name: "Select workflow..." })).toBeTruthy();
  });

  it("stays in the form when saving fails, and cancel returns to the detail", async () => {
    vi.mocked(updateTask).mockRejectedValue(new Error("x"));
    const { user } = setup([task({ id: 4 })]);
    await selectRow(user, 4);
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(updateTask).toHaveBeenCalled();
    expect(screen.getByText("Edit Task #4")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("Task #4")).toBeTruthy();
  });

  it("leaves the form when another row is selected", async () => {
    const { user } = setup([task({ id: 4 }), task({ id: 5 })]);
    await selectRow(user, 4);
    await user.click(screen.getByRole("button", { name: "Edit" }));
    await selectRow(user, 5);
    expect(screen.getByText("Task #5")).toBeTruthy();
  });
});
