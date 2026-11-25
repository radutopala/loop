import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { darkColors, lightColors } from "../../theme";
import { JSONCodeEditor } from "./JSONCodeEditor";

function viewOf(): EditorView {
  const dom = screen.getByTestId("config-json-editor").querySelector(".cm-editor") as HTMLElement;
  return EditorView.findFromDOM(dom)!;
}

function renderEditor(value: string) {
  const onChange = vi.fn();
  const onSave = vi.fn();
  const utils = render(<JSONCodeEditor value={value} onChange={onChange} onSave={onSave} colors={darkColors} />);
  return { onChange, onSave, ...utils };
}

describe("JSONCodeEditor", () => {
  it("shows the value in the editor", () => {
    renderEditor('{"a": 1}');
    expect(viewOf().state.doc.toString()).toBe('{"a": 1}');
  });

  it("reports a user edit through onChange", () => {
    const { onChange } = renderEditor("{}");
    act(() => viewOf().dispatch({ changes: { from: 1, insert: '"x": 2' } }));
    expect(onChange).toHaveBeenCalledWith('{"x": 2}');
  });

  it("replaces the doc when a new value comes in, without reporting it as an edit", () => {
    const { onChange, rerender, onSave } = renderEditor("{}");
    rerender(<JSONCodeEditor value='{"b": true}' onChange={onChange} onSave={onSave} colors={darkColors} />);
    expect(viewOf().state.doc.toString()).toBe('{"b": true}');
    expect(onChange).not.toHaveBeenCalled();
  });

  it("leaves the doc alone when the value matches it", () => {
    const { onChange, rerender, onSave } = renderEditor("{}");
    act(() => viewOf().dispatch({ changes: { from: 1, insert: " " } }));
    const before = viewOf().state;
    rerender(<JSONCodeEditor value="{ }" onChange={onChange} onSave={onSave} colors={darkColors} />);
    expect(viewOf().state).toBe(before);
  });

  it("calls the latest onSave on Mod-S", () => {
    const { onChange, rerender } = renderEditor("{}");
    const onSave = vi.fn();
    rerender(<JSONCodeEditor value="{}" onChange={onChange} onSave={onSave} colors={darkColors} />);
    const content = screen.getByTestId("config-json-editor").querySelector(".cm-content") as HTMLElement;
    fireEvent.keyDown(content, { key: "s", code: "KeyS", keyCode: 83, ctrlKey: true });
    expect(onSave).toHaveBeenCalledOnce();
  });

  it("reconfigures its theme when the colors change", () => {
    const { onChange, onSave, rerender } = renderEditor("{}");
    const view = viewOf();
    const darkClass = view.dom.className;
    rerender(<JSONCodeEditor value="{}" onChange={onChange} onSave={onSave} colors={lightColors} />);
    expect(viewOf()).toBe(view);
    expect(view.dom.className).not.toBe(darkClass);
  });

  it("destroys the editor on unmount", () => {
    const { unmount } = renderEditor("{}");
    const view = viewOf();
    const destroy = vi.spyOn(view, "destroy");
    unmount();
    expect(destroy).toHaveBeenCalledOnce();
  });
});
