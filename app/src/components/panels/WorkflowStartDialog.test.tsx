import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkflowDef } from "../../api/workflows";
import { darkColors } from "../../theme";
import { WorkflowStartDialog } from "./WorkflowStartDialog";

const deployDef: WorkflowDef = {
  name: "deploy",
  scope: "global",
  description: "Ship it",
  inputs: {
    env: { description: "Target environment", required: true, default: "staging" },
    tag: {},
  },
  nodes: [{ id: "a", type: "bash" }],
};

const jsonOf = (def: WorkflowDef) => {
  const { scope: _scope, ...rest } = def;
  return JSON.stringify(rest, null, 2);
};

type Props = Parameters<typeof WorkflowStartDialog>[0];

function setup(over: Partial<Props> = {}) {
  const props: Props = {
    show: true,
    startInputs: {},
    selectedStartDef: deployDef,
    colors: darkColors,
    onClose: vi.fn(),
    onInputChange: vi.fn(),
    onStart: vi.fn(),
    onSave: vi.fn().mockResolvedValue(null),
    testId: "dialog",
    ...over,
  };
  const view = render(<WorkflowStartDialog {...props} />);
  return { props, ...view };
}

const editor = () => screen.getByTestId("workflow-config-editor") as HTMLTextAreaElement;
const saveBtn = () => screen.getByTestId("workflow-config-save") as HTMLButtonElement;

describe("WorkflowStartDialog", () => {
  it("renders nothing when hidden or without a definition", () => {
    const { container, rerender, props } = setup({ show: false });
    expect(container.innerHTML).toBe("");
    rerender(<WorkflowStartDialog {...props} show selectedStartDef={undefined} />);
    expect(container.innerHTML).toBe("");
  });

  it("shows the workflow, its inputs and its definition without the scope tag", () => {
    setup({ startInputs: { env: "prod" } });
    expect(screen.getByText("deploy")).toBeTruthy();
    expect(screen.getByText("Ship it")).toBeTruthy();
    expect(screen.getByText("env *")).toBeTruthy();
    expect(screen.getByText("Target environment")).toBeTruthy();
    expect(screen.getByText("tag")).toBeTruthy();
    const env = screen.getByPlaceholderText("default: staging") as HTMLInputElement;
    expect(env.value).toBe("prod");
    const inputs = screen.getAllByRole("textbox").filter((el) => el.tagName === "INPUT") as HTMLInputElement[];
    expect(inputs[1]!.placeholder).toBe("");
    expect(inputs[1]!.value).toBe("");
    expect(editor().value).toBe(jsonOf(deployDef));
    expect(editor().value).not.toContain("scope");
    expect(saveBtn().disabled).toBe(true);
  });

  it("says when a workflow takes no inputs and has no description", () => {
    setup({ selectedStartDef: { name: "bare", description: "", nodes: [] } });
    expect(screen.getByText("This workflow takes no inputs.")).toBeTruthy();
    expect(screen.queryByText("Ship it")).toBeNull();
  });

  it("reports input edits through an updater", () => {
    // The updater reads the event value, so apply it while the event runs.
    let next: Record<string, string> = {};
    const onInputChange = vi.fn((fn: (prev: Record<string, string>) => Record<string, string>) => {
      next = fn({ tag: "v1" });
    });
    setup({ startInputs: { tag: "v1" }, onInputChange });
    fireEvent.change(screen.getByPlaceholderText("default: staging"), { target: { value: "qa" } });
    expect(onInputChange).toHaveBeenCalledOnce();
    expect(next).toEqual({ tag: "v1", env: "qa" });
  });

  it("starts, cancels, and closes from the backdrop but not from the card", async () => {
    const user = userEvent.setup();
    const { props } = setup();
    await user.click(screen.getByRole("button", { name: "Start" }));
    expect(props.onStart).toHaveBeenCalledOnce();
    await user.click(screen.getByText("Start Workflow"));
    expect(props.onClose).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("dialog"));
    expect(props.onClose).toHaveBeenCalledTimes(2);
  });

  it("closes on Escape only while shown, and ignores other keys", () => {
    const { props, rerender } = setup();
    fireEvent.keyDown(window, { key: "a" });
    expect(props.onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(editor(), { key: "Escape" });
    expect(props.onClose).toHaveBeenCalledOnce();
    rerender(<WorkflowStartDialog {...props} show={false} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(props.onClose).toHaveBeenCalledOnce();
  });

  it("saves an edited definition and shows it as saved until edited again", async () => {
    let resolve!: (v: string | null) => void;
    const onSave = vi.fn(() => new Promise<string | null>((r) => (resolve = r)));
    setup({ onSave });
    fireEvent.change(editor(), { target: { value: '{"name":"deploy2"}' } });
    expect(saveBtn().disabled).toBe(false);
    fireEvent.click(saveBtn());
    expect(onSave).toHaveBeenCalledWith('{"name":"deploy2"}');
    expect(saveBtn().textContent).toBe("Saving…");
    expect(saveBtn().disabled).toBe(true);
    await act(async () => resolve(null));
    expect(saveBtn().textContent).toBe("Save");
    // Still dirty relative to the prop, so no "saved" mark yet.
    expect(screen.queryByText("saved ✓")).toBeNull();
  });

  it("shows saved once the parent's definition matches the editor", async () => {
    const onSave = vi.fn().mockResolvedValue(null);
    const { props, rerender } = setup({ onSave });
    const next: WorkflowDef = { ...deployDef, description: "Ship it now" };
    fireEvent.change(editor(), { target: { value: jsonOf(next) } });
    await act(async () => fireEvent.click(saveBtn()));
    // Same name, so the reset effect does not wipe the saved flag.
    rerender(<WorkflowStartDialog {...props} onSave={onSave} selectedStartDef={next} />);
    expect(screen.getByText("saved ✓")).toBeTruthy();
    fireEvent.change(editor(), { target: { value: `${jsonOf(next)} ` } });
    expect(screen.queryByText("saved ✓")).toBeNull();
  });

  it("shows a save error and highlights the editor", async () => {
    const onSave = vi.fn().mockResolvedValue("invalid JSON");
    setup({ onSave });
    fireEvent.change(editor(), { target: { value: "{" } });
    await act(async () => fireEvent.click(saveBtn()));
    expect(screen.getByText("invalid JSON")).toBeTruthy();
    expect(editor().style.border).toContain("rgb(");
    expect(screen.queryByText("saved ✓")).toBeNull();
  });

  it("resets the editor when reopened on another workflow", () => {
    const { props, rerender } = setup();
    fireEvent.change(editor(), { target: { value: "scratch" } });
    const other: WorkflowDef = { name: "other", description: "", nodes: [] };
    rerender(<WorkflowStartDialog {...props} selectedStartDef={other} />);
    expect(editor().value).toBe(jsonOf(other));
  });
});
