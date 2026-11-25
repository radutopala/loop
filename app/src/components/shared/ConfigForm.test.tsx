import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConfigResponse, ConfigSchema } from "../../api/configApi";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors, lightColors } from "../../theme";
import { ConfigForm, type ConfigFormHandle, type ConfigFormProps, getSections, schemaToFields } from "./ConfigForm";

const colors = darkColors;

function cfg(content: Record<string, any> | null, raw?: string): ConfigResponse {
  return { path: "/home/u/.loop/config.json", content, raw: raw ?? (content ? `${JSON.stringify(content, null, 2)}\n` : "") };
}

function renderForm(props: Partial<ConfigFormProps> = {}) {
  const onSave = vi.fn<ConfigFormProps["onSave"]>().mockResolvedValue(null);
  const onDirtyChange = vi.fn();
  const ref = createRef<ConfigFormHandle>();
  const user = userEvent.setup();
  const all: ConfigFormProps = { schema: null, config: null, onSave, isGlobal: true, title: "Global Config", colors, onDirtyChange, ...props };
  const utils = render(<ConfigForm ref={ref} {...all} />);
  const rerender = (next: Partial<ConfigFormProps>) => utils.rerender(<ConfigForm ref={ref} {...all} {...next} />);
  return { onSave, onDirtyChange, ref, user, rerender };
}

/** The row a field renders into. */
function row(key: string): HTMLElement {
  return document.querySelector(`[data-config-key="${key}"]`) as HTMLElement;
}

function editor(): EditorView {
  return EditorView.findFromDOM(screen.getByTestId("config-json-editor").querySelector(".cm-editor") as HTMLElement)!;
}

function setEditorDoc(text: string) {
  const view = editor();
  act(() => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } }));
}

/** A color as the DOM reports it back from a style. */
function css(color: string): string {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
}

/** The input, select or textarea next to a sub-field's label within scope. */
function control(scope: HTMLElement, label: string): HTMLInputElement {
  const span = [...scope.querySelectorAll("span")].find((s) => s.textContent === label)!;
  return span.nextElementSibling as HTMLInputElement;
}

function saved(onSave: ReturnType<typeof vi.fn>, call = 0) {
  return JSON.parse(onSave.mock.calls[call]![0] as string);
}

describe("getSections", () => {
  it("is empty without a schema", () => {
    expect(getSections(null, true)).toEqual([]);
  });

  it("lists each section once, in field order", () => {
    const schema: ConfigSchema = {
      type: "object",
      properties: {
        a: { type: "string", "x-section": "One", "x-order": 1 },
        b: { type: "string", "x-section": "Two", "x-order": 2 },
        c: { type: "string", "x-section": "One", "x-order": 3 },
      },
    };
    expect(getSections(schema, true)).toEqual(["One", "Two"]);
  });
});

describe("schemaToFields", () => {
  it("flattens nested objects under their title and section, inheriting auto-save", () => {
    const schema: ConfigSchema = {
      type: "object",
      properties: {
        plain: { type: "string" },
        desktop: {
          type: "object",
          title: "Desktop",
          "x-section": "Appearance",
          "x-auto-save": true,
          properties: {
            theme: { type: "string", title: "Theme", "x-widget": "theme-picker" },
            size: { type: "integer", "x-auto-save": false },
          },
        },
        anon: { type: "object", properties: { inner: { type: "boolean" } } },
      },
    };
    const fields = schemaToFields(schema, true);
    expect(fields.map((f) => [f.key, f.label, f.section, f.autoSave])).toEqual([
      ["plain", "plain", "General", undefined],
      ["desktop.theme", "Desktop Theme", "Appearance", true],
      ["desktop.size", "Desktop size", "Appearance", false],
      ["anon.inner", "inner", "General", undefined],
    ]);
  });

  it("infers each field type from the schema", () => {
    const obj = { type: "object", properties: { n: { type: "string" } } };
    const schema: ConfigSchema = {
      type: "object",
      properties: {
        secret: { type: "string", "x-secret": true },
        choice: { type: "string", enum: ["a", 1] },
        flag: { type: "boolean" },
        count: { type: "integer" },
        ratio: { type: "number", "x-step": 0.1 },
        multi: { type: "array", items: { type: "string", enum: ["x", "y"] } },
        objs: { type: "array", items: obj },
        objsNoProps: { type: "array", items: { type: "object" } },
        list: { type: "array" },
        servers: { type: "object", additionalProperties: obj },
        env: { type: "object", additionalProperties: { type: "string" } },
        bare: { type: "object" },
      },
    };
    const types = Object.fromEntries(schemaToFields(schema, true).map((f) => [f.key, f.type]));
    expect(types).toEqual({
      secret: "password",
      choice: "dropdown",
      flag: "toggle",
      count: "number",
      ratio: "number",
      multi: "multiselect",
      objs: "objectarray",
      objsNoProps: "array",
      list: "array",
      servers: "objectmap",
      env: "keyvalue",
      bare: "text",
    });
    const fields = schemaToFields(schema, true);
    expect(fields.find((f) => f.key === "choice")?.options).toEqual(["a", "1"]);
    expect(fields.find((f) => f.key === "multi")?.options).toEqual(["x", "y"]);
    expect(fields.find((f) => f.key === "objs")?.itemProperties).toEqual(obj.properties);
    expect(fields.find((f) => f.key === "servers")?.itemProperties).toEqual(obj.properties);
  });
});

describe("ConfigForm", () => {
  beforeEach(() => {
    vi.useRealTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("shows a loading state until the schema arrives", () => {
    renderForm({ title: "Project Config" });
    expect(screen.getByText("Project Config")).toBeTruthy();
    expect(screen.getByText("Loading schema...")).toBeTruthy();
  });

  describe("form and JSON views", () => {
    const schema: ConfigSchema = {
      type: "object",
      properties: {
        name: { type: "string", title: "Name", description: "Display name", "x-section": "General", "x-order": 1 },
        tags: { type: "array", items: { type: "string" }, "x-section": "Lists", "x-order": 2 },
        nested: { type: "object", properties: { deep: { type: "string" } }, "x-section": "Lists", "x-order": 3 },
      },
    };

    it("renders the title, path, section headers and field labels", () => {
      renderForm({ schema, config: cfg({ name: "loop" }) });
      expect(screen.getByText("Global Config")).toBeTruthy();
      expect(screen.getByText("/home/u/.loop/config.json")).toBeTruthy();
      expect(screen.getByText("General")).toBeTruthy();
      expect(screen.getByText("Lists")).toBeTruthy();
      expect(screen.getByText("Display name")).toBeTruthy();
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("loop");
    });

    it("switches to JSON with the cleaned form data, and back with the parsed draft", async () => {
      const { user } = renderForm({ schema, config: cfg({ name: "", tags: ["", "a"], nested: { deep: "" }, other: null }) });
      await user.click(screen.getByRole("button", { name: "Form" }));
      expect(screen.queryByTestId("config-json-editor")).toBeNull();
      await user.click(screen.getByRole("button", { name: "JSON" }));
      expect(JSON.parse(editor().state.doc.toString())).toEqual({ tags: ["a"] });
      expect(screen.getByText("Ctrl+S to save")).toBeTruthy();

      setEditorDoc('{"name": "edited"}');
      await user.click(screen.getByRole("button", { name: "Form" }));
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("edited");
    });

    it("serializes an all-empty form as an empty object", async () => {
      const { user } = renderForm({ schema, config: cfg({ tags: [""] }) });
      await user.click(screen.getByRole("button", { name: "JSON" }));
      expect(editor().state.doc.toString()).toBe("{}\n");
    });

    it("stays in JSON and shows the error when the draft doesn't parse", async () => {
      const { user } = renderForm({ schema, config: cfg({}) });
      await user.click(screen.getByRole("button", { name: "JSON" }));
      setEditorDoc("{nope");
      await user.click(screen.getByRole("button", { name: "Form" }));
      expect(screen.getByText(/^Invalid JSON: /)).toBeTruthy();
      expect(screen.getByTestId("config-json-editor")).toBeTruthy();
    });

    it("shows the Mac save shortcut on a Mac", async () => {
      vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
      renderForm({ schema, config: cfg({}), jsonOnly: true });
      expect(screen.getByText("\u2318S to save")).toBeTruthy();
    });

    it("in JSON-only mode shows the editor and the path, without the view toggle", () => {
      renderForm({ schema, config: cfg({ a: 1 }, '{"a": 1}\n'), jsonOnly: true });
      expect(screen.queryByRole("button", { name: "Form" })).toBeNull();
      expect(screen.getByText("/home/u/.loop/config.json")).toBeTruthy();
      expect(editor().state.doc.toString()).toBe('{"a": 1}\n');
    });

    it("in JSON-only mode without a config shows an empty object and no path", () => {
      renderForm({ schema, config: null, jsonOnly: true });
      expect(editor().state.doc.toString()).toBe("{\n  \n}\n");
      expect(screen.queryByText("/home/u/.loop/config.json")).toBeNull();
    });

    it("builds the JSON draft from the content when a reloaded config has no raw text", async () => {
      const { user, rerender } = renderForm({ schema, config: cfg({ name: "a" }) });
      rerender({ config: { path: "p", content: null, raw: undefined as unknown as string } });
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("");
      await user.click(screen.getByRole("button", { name: "JSON" }));
      expect(editor().state.doc.toString()).toBe("{}\n");
    });

    it("shows only the visible section's fields, without headers", () => {
      renderForm({ schema, config: cfg({}), visibleSection: "Lists" });
      expect(row("name")).toBeNull();
      expect(row("tags")).toBeTruthy();
      expect(screen.queryByText("Lists")).toBeNull();
      expect(screen.queryByRole("button", { name: "JSON" })).toBeNull();
    });

    it("saves the form as cleaned JSON and clears dirty", async () => {
      const { user, ref, onSave, onDirtyChange } = renderForm({ schema, config: cfg({ name: "a" }) });
      await user.type(row("name").querySelector("input")!, "b");
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);
      await act(() => ref.current!.save());
      expect(saved(onSave)).toEqual({ name: "ab" });
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    });

    it("saves the JSON draft as typed", async () => {
      const { user, ref, onSave, onDirtyChange } = renderForm({ schema, config: cfg({}) });
      await user.click(screen.getByRole("button", { name: "JSON" }));
      setEditorDoc('{"x": 1}');
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);
      await act(() => ref.current!.save());
      expect(onSave).toHaveBeenCalledWith('{"x": 1}');
    });

    it("shows a save error and stays dirty", async () => {
      const { ref, onSave, onDirtyChange } = renderForm({ schema, config: cfg({}) });
      onSave.mockResolvedValue("permission denied");
      onDirtyChange.mockClear();
      await act(() => ref.current!.save());
      expect(screen.getByText("permission denied")).toBeTruthy();
      expect(onDirtyChange).not.toHaveBeenCalled();
    });

    it("cancel restores the loaded config", async () => {
      const { user, ref, onDirtyChange } = renderForm({ schema, config: cfg({ name: "a" }) });
      await user.type(row("name").querySelector("input")!, "b");
      act(() => ref.current!.cancel());
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("a");
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
    });

    it("cancel rebuilds the JSON draft when the config has no raw text", async () => {
      const { user, ref } = renderForm({ schema, config: { path: "p", content: { name: "a" }, raw: undefined as unknown as string } });
      await user.click(screen.getByRole("button", { name: "JSON" }));
      setEditorDoc("{}");
      act(() => ref.current!.cancel());
      expect(JSON.parse(editor().state.doc.toString())).toEqual({ name: "a" });
    });

    it("cancel shows an empty form when the loaded config has no content", async () => {
      const { user, ref } = renderForm({ schema, config: cfg(null, "") });
      await user.type(row("name").querySelector("input")!, "b");
      act(() => ref.current!.cancel());
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("");
    });

    it("cancel without a config empties the form", async () => {
      const { user, ref } = renderForm({ schema, config: null });
      await user.type(row("name").querySelector("input")!, "b");
      act(() => ref.current!.cancel());
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("");
    });

    it("keeps edits when the config is re-fetched unchanged, and reloads a new one", async () => {
      const { user, rerender } = renderForm({ schema, config: cfg({ name: "a" }) });
      await user.type(row("name").querySelector("input")!, "b");
      rerender({ config: cfg({ name: "a" }) });
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("ab");
      rerender({ config: cfg({ name: "z" }) });
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("z");
      rerender({ config: null });
      expect((row("name").querySelector("input") as HTMLInputElement).value).toBe("");
    });
  });

  describe("auto-save", () => {
    const schema: ConfigSchema = {
      type: "object",
      properties: {
        font: { type: "integer", "x-widget": "stepper", "x-section": "Look", "x-auto-save": true },
        other: { type: "string", "x-section": "Else" },
      },
    };

    it("saves shortly after a change when every visible field auto-saves", async () => {
      vi.useFakeTimers();
      const { onSave, onDirtyChange } = renderForm({ schema, config: cfg({ font: 13 }), visibleSection: "Look" });
      onDirtyChange.mockClear();
      fireEvent.click(screen.getByRole("button", { name: "+" }));
      fireEvent.click(screen.getByRole("button", { name: "+" }));
      expect(onSave).not.toHaveBeenCalled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(100);
      });
      expect(onSave).toHaveBeenCalledOnce();
      expect(saved(onSave)).toEqual({ font: 15 });
      expect(onDirtyChange).not.toHaveBeenCalledWith(true);
    });

    it("marks the form dirty instead when some visible field doesn't auto-save", () => {
      const { onSave, onDirtyChange } = renderForm({ schema, config: cfg({ font: 13 }) });
      fireEvent.click(screen.getByRole("button", { name: "+" }));
      expect(onDirtyChange).toHaveBeenLastCalledWith(true);
      expect(onSave).not.toHaveBeenCalled();
    });

    it("doesn't auto-save a section with no fields", () => {
      const { onDirtyChange } = renderForm({ schema, config: cfg({}), visibleSection: "Missing" });
      expect(onDirtyChange).toHaveBeenLastCalledWith(false);
      expect(row("font")).toBeNull();
    });
  });

  describe("mount toggles", () => {
    const schema: ConfigSchema = {
      type: "object",
      properties: {
        mount_gh: { type: "boolean", "x-mount": "~/.config/gh:/home/agent/.config/gh:ro" },
        mounts: { type: "array", items: { type: "string" } },
        inherit_mounts: { type: "boolean" },
      },
    };

    it("adds the toggle's mount to the mounts list", async () => {
      const { user, ref, onSave } = renderForm({ schema, config: cfg({}) });
      await user.click(row("mount_gh").firstElementChild as HTMLElement);
      await act(() => ref.current!.save());
      expect(saved(onSave)).toEqual({ mount_gh: true, mounts: ["~/.config/gh:/home/agent/.config/gh:ro"] });
    });

    it("doesn't add a mount the inherited global config already has", async () => {
      const { user, ref, onSave } = renderForm({ schema, config: cfg({}), isGlobal: false, inheritedConfig: { mounts: ["/x:/home/agent/.config/gh"] } });
      await user.click(row("mount_gh").firstElementChild as HTMLElement);
      await act(() => ref.current!.save());
      expect(saved(onSave)).toEqual({ mount_gh: true });
    });

    it("adds the mount when the project doesn't inherit the global mounts", async () => {
      const { user, ref, onSave } = renderForm({ schema, config: cfg({ inherit_mounts: false }), inheritedConfig: { mounts: ["/x:/home/agent/.config/gh"] } });
      await user.click(row("mount_gh").firstElementChild as HTMLElement);
      await act(() => ref.current!.save());
      expect(saved(onSave).mounts).toEqual(["~/.config/gh:/home/agent/.config/gh:ro"]);
    });

    it("ignores inherited config without a mounts list", async () => {
      const { user, ref, onSave } = renderForm({ schema, config: cfg({}), inheritedConfig: { other: "x" } });
      await user.click(row("mount_gh").firstElementChild as HTMLElement);
      await act(() => ref.current!.save());
      expect(saved(onSave).mounts).toEqual(["~/.config/gh:/home/agent/.config/gh:ro"]);
    });
  });

  describe("fields", () => {
    function single(prop: ConfigSchema["properties"][string], content: Record<string, any> = {}, extra: Partial<ConfigFormProps> = {}) {
      const schema: ConfigSchema = { type: "object", properties: { f: prop } };
      return renderForm({ schema, config: cfg(content), ...extra });
    }

    async function saveAndRead(ref: React.RefObject<ConfigFormHandle | null>, onSave: ReturnType<typeof vi.fn>) {
      await act(() => ref.current!.save());
      return saved(onSave, onSave.mock.calls.length - 1);
    }

    it("text: edits the value and shows the placeholder", async () => {
      const { user, ref, onSave } = single({ type: "string", "x-placeholder": "type here" });
      const input = row("f").querySelector("input")!;
      expect(input.placeholder).toBe("type here");
      await user.type(input, "hi");
      expect(await saveAndRead(ref, onSave)).toEqual({ f: "hi" });
    });

    it("password: hides the value until shown", async () => {
      const { user, ref, onSave } = single({ type: "string", "x-secret": true }, { f: "s3cret" });
      const input = row("f").querySelector("input")!;
      expect(input.type).toBe("password");
      await user.click(screen.getByTitle("Show"));
      expect(input.type).toBe("text");
      await user.click(screen.getByTitle("Hide"));
      expect(input.type).toBe("password");
      await user.type(input, "!");
      expect(await saveAndRead(ref, onSave)).toEqual({ f: "s3cret!" });
    });

    it("password: starts empty when unset", () => {
      single({ type: "string", "x-secret": true });
      expect((row("f").querySelector("input") as HTMLInputElement).value).toBe("");
    });

    it("number: sets a number and clears to unset", async () => {
      const { ref, onSave } = single({ type: "number", "x-step": 0.5 }, { f: 2 });
      const input = row("f").querySelector("input") as HTMLInputElement;
      expect(input.step).toBe("0.5");
      fireEvent.change(input, { target: { value: "3.5" } });
      expect(await saveAndRead(ref, onSave)).toEqual({ f: 3.5 });
      fireEvent.change(input, { target: { value: "" } });
      expect(await saveAndRead(ref, onSave)).toEqual({});
    });

    it("number: starts empty when unset", () => {
      single({ type: "integer" });
      expect((row("f").querySelector("input") as HTMLInputElement).value).toBe("");
    });

    it.each([
      ["its value", { f: true }, undefined, undefined, true],
      ["the inherited value", {}, { f: true }, false, true],
      ["the schema default", {}, undefined, true, true],
      ["off", {}, undefined, undefined, false],
    ])("toggle: shows %s and flips it", async (_name, content, inheritedConfig, def, on) => {
      const { user, ref, onSave } = single({ type: "boolean", default: def }, content, { inheritedConfig });
      await user.click(row("f").firstElementChild as HTMLElement);
      expect(await saveAndRead(ref, onSave)).toEqual({ f: !on });
    });

    it("dropdown: lists the options with their labels and picks one", async () => {
      const { user, ref, onSave } = single({ type: "string", enum: ["", "en", "ro"], "x-enum-labels": { ro: "Romanian" } }, { f: "en" });
      const select = row("f").querySelector("select")!;
      expect([...select.options].map((o) => o.textContent)).toEqual(["(default)", "en", "Romanian"]);
      expect(select.value).toBe("en");
      await user.selectOptions(select, "ro");
      expect(await saveAndRead(ref, onSave)).toEqual({ f: "ro" });
    });

    it("dropdown: shows the first option when unset", () => {
      single({ type: "string", enum: ["a", "b"] });
      expect((row("f").querySelector("select") as HTMLSelectElement).value).toBe("a");
    });

    it("multiselect: toggles options on and off", async () => {
      const { user, ref, onSave } = single({ type: "array", items: { type: "string", enum: ["x", "y"] } }, { f: ["x"] });
      await user.click(screen.getByRole("button", { name: "y" }));
      expect(await saveAndRead(ref, onSave)).toEqual({ f: ["x", "y"] });
      await user.click(screen.getByRole("button", { name: "x" }));
      expect(await saveAndRead(ref, onSave)).toEqual({ f: ["y"] });
    });

    it("multiselect: treats a non-array value as nothing selected", async () => {
      const { user, ref, onSave } = single({ type: "array", items: { type: "string", enum: ["x"] } }, { f: "x" });
      await user.click(screen.getByRole("button", { name: "x" }));
      expect(await saveAndRead(ref, onSave)).toEqual({ f: ["x"] });
    });

    it("multiselect: starts with nothing selected when unset", async () => {
      const { user, ref, onSave } = single({ type: "array", items: { type: "string", enum: ["x"] } });
      await user.click(screen.getByRole("button", { name: "x" }));
      expect(await saveAndRead(ref, onSave)).toEqual({ f: ["x"] });
    });

    describe("array", () => {
      it("adds trimmed items by Enter or the add button, skipping blanks, and removes them", async () => {
        const { user, ref, onSave } = single({ type: "array", items: { type: "string" } }, { f: ["a", { o: 1 }] });
        expect(screen.getByText('{"o":1}')).toBeTruthy();
        const input = row("f").querySelector("input")!;
        expect(input.placeholder).toBe("Add item...");
        await user.type(input, "  b  {Enter}");
        expect(input.value).toBe("");
        await user.type(input, "   ");
        const buttons = row("f").querySelectorAll("button");
        await user.click(buttons[buttons.length - 1]!);
        await user.type(input, "c");
        await user.click(buttons[buttons.length - 1]!);
        expect(await saveAndRead(ref, onSave)).toEqual({ f: ["a", { o: 1 }, "b", "c"] });
        await user.click(row("f").querySelectorAll("button")[0]!);
        expect(await saveAndRead(ref, onSave)).toEqual({ f: [{ o: 1 }, "b", "c"] });
      });

      it("ignores keys other than Enter and uses the field's placeholder", async () => {
        const { user, onSave } = single({ type: "array", "x-placeholder": "/path" });
        const input = row("f").querySelector("input")!;
        expect(input.placeholder).toBe("/path");
        await user.type(input, "x{Tab}");
        expect(input.value).toBe("x");
        expect(onSave).not.toHaveBeenCalled();
      });

      it("treats a non-array value as empty", () => {
        single({ type: "array" }, { f: "oops" });
        expect(row("f").querySelectorAll("button")).toHaveLength(1);
      });

      it("offers Browse for directory lists in the desktop app", async () => {
        const showOpenDirectoryDialog = vi.fn().mockResolvedValueOnce("/picked").mockResolvedValueOnce(null);
        window.loopAPI = { showOpenDirectoryDialog } as unknown as typeof window.loopAPI;
        try {
          const schema: ConfigSchema = {
            type: "object",
            properties: { extra_dirs: { type: "array" }, memory: { type: "object", properties: { paths: { type: "array" } } }, other: { type: "array" } },
          };
          const { user, ref, onSave } = renderForm({ schema, config: cfg({}) });
          expect(row("other").textContent).not.toContain("Browse...");
          expect(row("memory.paths").textContent).toContain("Browse...");
          const browse = screen.getAllByRole("button", { name: "Browse..." })[0]!;
          fireEvent.mouseEnter(browse);
          expect(browse.style.borderColor).toBe(css(colors.textDim));
          fireEvent.mouseLeave(browse);
          expect(browse.style.borderColor).toBe(css(colors.border));
          await user.click(browse);
          await user.click(browse);
          expect(showOpenDirectoryDialog).toHaveBeenCalledTimes(2);
          expect(await saveAndRead(ref, onSave)).toEqual({ extra_dirs: ["/picked"] });
        } finally {
          delete (window as { loopAPI?: unknown }).loopAPI;
        }
      });

      it("hides Browse outside the desktop app", () => {
        single({ type: "array" });
        const schema: ConfigSchema = { type: "object", properties: { extra_dirs: { type: "array" } } };
        renderForm({ schema, config: cfg({}) });
        expect(screen.queryByRole("button", { name: "Browse..." })).toBeNull();
      });
    });

    it("add and remove buttons highlight on hover", () => {
      single({ type: "array" }, { f: ["a"] });
      const [remove, add] = [...row("f").querySelectorAll("button")] as [HTMLButtonElement, HTMLButtonElement];
      fireEvent.mouseEnter(remove);
      expect((remove as HTMLElement).style.color).toBe(css(colors.error));
      fireEvent.mouseLeave(remove);
      expect((remove as HTMLElement).style.color).toBe(css(colors.textDim));
      fireEvent.mouseEnter(add);
      expect((add as HTMLElement).style.borderColor).toBe(css(colors.textDim));
      fireEvent.mouseLeave(add);
      expect((add as HTMLElement).style.borderColor).toBe(css(colors.border));
    });

    describe("keyvalue", () => {
      const prop = { type: "object", additionalProperties: { type: "string" } };

      it("lists entries, adds one by Enter from either input or the button, and removes one", async () => {
        const { user, ref, onSave } = single(prop, { f: { A: "1" } });
        expect(screen.getByText("A")).toBeTruthy();
        const [k, v] = [...row("f").querySelectorAll("input")] as [HTMLInputElement, HTMLInputElement];
        await user.type(k, " B ");
        await user.type(v, "2{Enter}");
        expect(await saveAndRead(ref, onSave)).toEqual({ f: { A: "1", B: "2" } });
        await user.type(k, "C{Enter}");
        await user.type(k, "x{Tab}");
        await user.type(v, "y{Tab}");
        const buttons = row("f").querySelectorAll("button");
        await user.click(buttons[buttons.length - 1]!);
        // C has an empty value, which saving drops.
        expect(screen.getByText("C")).toBeTruthy();
        expect(await saveAndRead(ref, onSave)).toEqual({ f: { A: "1", B: "2", x: "y" } });
        await user.click(row("f").querySelectorAll("button")[0]!);
        expect(await saveAndRead(ref, onSave)).toEqual({ f: { B: "2", x: "y" } });
      });

      it("skips an entry without a key", async () => {
        const { user, onDirtyChange } = single(prop);
        onDirtyChange.mockClear();
        const [, v] = [...row("f").querySelectorAll("input")] as [HTMLInputElement, HTMLInputElement];
        await user.type(v, "val{Enter}");
        expect(onDirtyChange).not.toHaveBeenCalled();
        expect((v as HTMLInputElement).value).toBe("val");
      });
    });

    describe("objectarray", () => {
      const nestedObj = { type: "object", properties: { x: { type: "string" } } };
      const prop = {
        type: "array",
        title: "Rules",
        items: {
          type: "object",
          properties: {
            name: { type: "string", title: "Name", "x-order": 1, "x-placeholder": "rule name" },
            plain: { type: "string", "x-order": 2 },
            count: { type: "integer", title: "Count", "x-order": 3 },
            ratio: { type: "number", "x-order": 4 },
            on: { type: "boolean", title: "On", "x-order": 5 },
            off: { type: "boolean", "x-order": 6 },
            kind: { type: "string", enum: ["a", "b"], title: "Kind", "x-order": 7 },
            kind2: { type: "string", enum: ["c"], "x-order": 8 },
            body: { type: "string", "x-widget": "textarea", title: "Body", "x-placeholder": "prompt", "x-order": 9 },
            body2: { type: "string", "x-widget": "textarea", "x-order": 10 },
            args: { type: "array", items: { type: "string" }, title: "Args", "x-order": 11 },
            args2: { type: "array", items: { type: "string" }, "x-order": 12 },
            subs: { type: "array", items: nestedObj, title: "Subs", "x-order": 13 },
            subs2: { type: "array", items: nestedObj, "x-order": 14 },
            map: { type: "object", additionalProperties: nestedObj, title: "Map", "x-order": 15 },
            map2: { type: "object", additionalProperties: nestedObj, "x-order": 16 },
            limits: {
              type: "object",
              title: "Limits",
              "x-order": 17,
              properties: { max: { type: "integer", title: "Max", "x-order": 1 }, unit: { type: "string", "x-placeholder": "ms", "x-order": 2 } },
            },
            limits2: { type: "object", properties: { v: { type: "string" }, w: { type: "string" } }, "x-order": 18 },
            other: { type: "null" },
          },
        },
      };

      it("adds an item with empty values per type", async () => {
        const { user, ref, onSave } = single(prop);
        const buttons = row("f").querySelectorAll("button");
        await user.click(buttons[buttons.length - 1]!);
        await act(() => ref.current!.save());
        expect(saved(onSave)).toEqual({
          f: [
            {
              name: "",
              plain: "",
              count: 0,
              ratio: 0,
              on: false,
              off: false,
              kind: "",
              kind2: "",
              body: "",
              body2: "",
              args: [],
              args2: [],
              subs: [],
              subs2: [],
              map: {},
              map2: {},
              limits: {},
              limits2: {},
            },
          ],
        });
        expect(screen.getByText("Rules")).toBeTruthy();
        expect(screen.getByPlaceholderText("rule name")).toBeTruthy();
        expect(screen.getByPlaceholderText("prompt")).toBeTruthy();
        expect(screen.getByPlaceholderText("ms")).toBeTruthy();
        for (const label of ["Name", "plain", "Count", "On", "off", "Kind", "kind2", "Body", "body2", "Args", "args2", "Subs", "subs2", "Map", "map2", "Limits", "limits2", "Max", "v", "other"]) {
          expect(screen.getAllByText(label).length).toBeGreaterThan(0);
        }
      });

      it("edits each kind of sub-field", async () => {
        const { user, ref, onSave } = single(prop, { f: [{ name: "a" }, { name: "b" }] });
        const card = [...row("f").children].find((c) => c.querySelector("select")) as HTMLElement;
        await user.type(control(card, "Name"), "1");
        fireEvent.change(control(card, "plain"), { target: { value: "p" } });
        fireEvent.change(control(card, "Count"), { target: { value: "7" } });
        fireEvent.change(control(card, "ratio"), { target: { value: "x" } });
        fireEvent.change(control(card, "other"), { target: { value: "o" } });
        await user.click(control(card, "On"));
        await user.selectOptions(control(card, "Kind"), "b");
        expect([...(control(card, "kind2") as unknown as HTMLSelectElement).options].map((o) => o.textContent)).toEqual(["\u2014", "c"]);
        fireEvent.change(control(card, "Body"), { target: { value: "hello" } });
        fireEvent.change(control(card, "body2"), { target: { value: "there" } });
        fireEvent.change(control(card, "Max"), { target: { value: "9" } });
        fireEvent.change(control(card, "unit"), { target: { value: "s" } });
        fireEvent.change(control(card, "v"), { target: { value: "w" } });
        await act(() => ref.current!.save());
        expect(saved(onSave).f).toEqual([
          { name: "a1", plain: "p", count: 7, ratio: "x", other: "o", on: true, kind: "b", body: "hello", body2: "there", limits: { max: 9, unit: "s" }, limits2: { v: "w" } },
          { name: "b" },
        ]);
      });

      it("edits nested arrays, object arrays and maps inside an item", async () => {
        const { user, ref, onSave } = single(prop, { f: [{}] });
        const card = [...row("f").children].find((c) => c.querySelector("select")) as HTMLElement;
        const addInputs = card.querySelectorAll<HTMLInputElement>('input[placeholder="Add..."]');
        await user.type(addInputs[0]!, "--x{Enter}");
        await user.type(addInputs[1]!, "--y{Enter}");
        const mapInputs = card.querySelectorAll<HTMLInputElement>('input[placeholder="Add map..."]');
        await user.type(mapInputs[0]!, "k1{Enter}");
        await user.type(card.querySelector<HTMLInputElement>('input[placeholder="Add map2..."]')!, "k2{Enter}");
        await act(() => ref.current!.save());
        expect(saved(onSave).f[0]!).toEqual({ args: ["--x"], args2: ["--y"], map: { k1: { x: "" } }, map2: { k2: { x: "" } } });
      });

      it("adds and edits a nested object array item", async () => {
        const nested = {
          type: "array",
          items: { type: "object", properties: { subs: { type: "array", items: { type: "object", properties: { x: { type: "string", title: "X" } } } } } },
        };
        const { user, ref, onSave } = single(nested, { f: [{}] });
        const buttons = () => [...row("f").querySelectorAll("button")];
        // Buttons: outer remove, inner add, outer add.
        await user.click(buttons()[1]!);
        const input = row("f").querySelector<HTMLInputElement>("input")!;
        await user.type(input, "v");
        await act(() => ref.current!.save());
        expect(saved(onSave)).toEqual({ f: [{ subs: [{ x: "v" }] }] });
      });

      it("removes an item", async () => {
        // Sub-fields without an x-order keep their schema order.
        const { user, ref, onSave } = single({ type: "array", items: { type: "object", properties: { n: { type: "string" }, m: { type: "string" } } } }, { f: [{ n: "a" }, { n: "b" }] });
        const labels = [...row("f").querySelectorAll("span")].map((x) => x.textContent);
        expect(labels.slice(0, 2)).toEqual(["n", "m"]);
        await user.click(row("f").querySelectorAll("button")[0]!);
        expect(await saveAndRead(ref, onSave)).toEqual({ f: [{ n: "b" }] });
      });

      it("treats a non-array value as no items", () => {
        single({ type: "array", items: { type: "object", properties: { n: { type: "string" } } } }, { f: "bad" });
        expect(row("f").querySelectorAll("button")).toHaveLength(1);
      });
    });

    describe("objectmap", () => {
      const prop = {
        type: "object",
        title: "MCP Servers",
        additionalProperties: {
          type: "object",
          properties: {
            command: { type: "string", title: "Command", "x-placeholder": "npx", "x-order": 1 },
            url: { type: "string", "x-order": 2 },
            args: { type: "array", items: { type: "string" }, title: "Args", "x-order": 3 },
            args2: { type: "array", items: { type: "string" }, "x-order": 4 },
            env: { type: "object", additionalProperties: { type: "string" }, title: "Env", "x-order": 5 },
            env2: { type: "object", additionalProperties: { type: "string" }, "x-order": 6 },
            meta: { type: "object", "x-order": 7 },
            n: { type: "integer", "x-order": 8 },
          },
        },
      };

      it("adds a named entry with empty values, refusing blank and duplicate names", async () => {
        const { user, ref, onSave, onDirtyChange } = single(prop, { f: { a: { command: "x" } } });
        const input = screen.getByPlaceholderText("Add mcp servers...");
        onDirtyChange.mockClear();
        await user.type(input, "   {Enter}");
        await user.type(input, "{selectall}{backspace}a{Enter}");
        expect(onDirtyChange).not.toHaveBeenCalled();
        await user.type(input, "{selectall}{backspace} b {Tab}");
        const buttons = row("f").querySelectorAll("button");
        await user.click(buttons[buttons.length - 1]!);
        expect((input as HTMLInputElement).value).toBe("");
        expect(screen.getByText("b")).toBeTruthy();
        expect(await saveAndRead(ref, onSave)).toEqual({ f: { a: { command: "x" } } });
      });

      it("edits an entry's text, array and key-value properties", async () => {
        const { user, ref, onSave } = single(prop, { f: { srv: { command: "node", args: ["a"], env: { K: "1" } }, empty: null } });
        expect(screen.getAllByPlaceholderText("npx")).toHaveLength(2);
        const card = screen.getByText("srv").parentElement!.parentElement as HTMLElement;
        const text = card.querySelectorAll<HTMLInputElement>(':scope > div > input[type="text"]');
        await user.type(text[0]!, "!");
        await user.type(text[1]!, "http://h");
        const adds = card.querySelectorAll<HTMLInputElement>('input[placeholder="Add..."]');
        await user.type(adds[0]!, " b {Enter}");
        await user.type(adds[0]!, "  {Enter}");
        await user.type(adds[1]!, "z{Tab}");
        const addButtons = [...card.querySelectorAll("button")].filter((b) => b.querySelector("line[x1='12']"));
        await user.click(addButtons[1]!);
        const keys = card.querySelectorAll<HTMLInputElement>('input[placeholder="KEY"]');
        const vals = card.querySelectorAll<HTMLInputElement>('input[placeholder="value"]');
        await user.type(keys[0]!, " J ");
        await user.type(vals[0]!, "2{Enter}");
        await user.type(keys[0]!, "   {Enter}");
        await user.type(keys[1]!, "L{Enter}");
        await user.type(keys[1]!, "M{Tab}");
        await user.type(vals[1]!, "3{Tab}");
        await user.click(addButtons[3]!);
        await user.type(keys[0]!, "Q{Tab}");
        await user.type(vals[0]!, "q{Tab}");
        await user.click(addButtons[2]!);
        expect(await saveAndRead(ref, onSave)).toEqual({
          f: { srv: { command: "node!", url: "http://h", args: ["a", "b"], args2: ["z"], env: { K: "1", J: "2", Q: "q" }, env2: { M: "3" } } },
        });

        // Remove an array item and an env entry by their x marks.
        const argTag = screen.getByText("a", { selector: "span" });
        await user.click(argTag.querySelector("span")!);
        const envKey = screen.getByText("K");
        await user.click(envKey.parentElement!.querySelector("span:last-child")!);
        expect(await saveAndRead(ref, onSave)).toEqual({
          f: { srv: { command: "node!", url: "http://h", args: ["b"], args2: ["z"], env: { J: "2", Q: "q" }, env2: { M: "3" } } },
        });
      });

      it("removes an entry", async () => {
        const unordered = { type: "object", additionalProperties: { type: "object", properties: { command: { type: "string" }, url: { type: "string" } } } };
        const { user, ref, onSave } = single(unordered, { f: { a: { command: "x" }, b: { command: "y" } } });
        const header = screen.getByText("a").parentElement!;
        await user.click(header.querySelector("button")!);
        expect(await saveAndRead(ref, onSave)).toEqual({ f: { b: { command: "y" } } });
      });
    });

    describe("theme picker", () => {
      const prop = { type: "string", "x-widget": "theme-picker" };
      const custom = { ...lightColors, bg: "#123456" };

      it("highlights the current theme and picks another", async () => {
        const schema: ConfigSchema = { type: "object", properties: { f: prop } };
        const onSave = vi.fn().mockResolvedValue(null);
        const ref = createRef<ConfigFormHandle>();
        render(
          <ThemeProvider initialTheme="dark" customThemes={{ ocean: custom }}>
            <ConfigForm ref={ref} schema={schema} config={cfg({})} onSave={onSave} isGlobal title="t" colors={colors} />
          </ThemeProvider>,
        );
        const dark = screen.getByRole("button", { name: "Dark" });
        expect(dark.style.border).toContain("2px solid");
        expect(dark.style.borderColor).toBe(css(colors.active));
        expect(screen.getByRole("button", { name: "Light" }).style.borderColor).toBe(css(colors.border));
        await userEvent.click(screen.getByRole("button", { name: "Ocean" }));
        await act(() => ref.current!.save());
        expect(saved(onSave)).toEqual({ f: "ocean" });
        expect(screen.getByRole("button", { name: "Ocean" }).style.borderColor).toBe(css(colors.active));
      });
    });

    describe("stepper", () => {
      it.each([
        ["its value", { f: 20 }, undefined, "20px"],
        ["the schema default", {}, 15, "15px"],
        ["13px otherwise", {}, undefined, "13px"],
      ])("shows %s", (_n, content, def, text) => {
        single({ type: "integer", "x-widget": "stepper", default: def }, content);
        expect(screen.getByText(text)).toBeTruthy();
      });

      it("steps within 8 to 30", async () => {
        const { user, ref, onSave, rerender } = single({ type: "integer", "x-widget": "stepper" }, { f: 8 });
        await user.click(screen.getByRole("button", { name: "\u2212" }));
        expect(await saveAndRead(ref, onSave)).toEqual({ f: 8 });
        rerender({ config: cfg({ f: 30 }) });
        await user.click(screen.getByRole("button", { name: "+" }));
        expect(await saveAndRead(ref, onSave)).toEqual({ f: 30 });
        await user.click(screen.getByRole("button", { name: "\u2212" }));
        expect(await saveAndRead(ref, onSave)).toEqual({ f: 29 });
      });
    });

    it("reads a nested value through a non-object parent as unset, and writes through it", async () => {
      const schema: ConfigSchema = { type: "object", properties: { a: { type: "object", properties: { b: { type: "string" } } } } };
      const { user, ref, onSave } = renderForm({ schema, config: cfg({ a: "flat" }) });
      const input = row("a.b").querySelector("input")!;
      expect(input.value).toBe("");
      await user.type(input, "x");
      expect(await saveAndRead(ref, onSave)).toEqual({ a: { b: "x" } });
    });

    it("writes into an existing nested object", async () => {
      const schema: ConfigSchema = { type: "object", properties: { a: { type: "object", properties: { b: { type: "string" }, c: { type: "string" } } } } };
      const { user, ref, onSave } = renderForm({ schema, config: cfg({ a: { c: "keep" } }) });
      await user.type(row("a.b").querySelector("input")!, "x");
      expect(await saveAndRead(ref, onSave)).toEqual({ a: { b: "x", c: "keep" } });
    });
  });
});
