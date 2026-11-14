import { describe, expect, it, vi } from "vitest";
import { type PageContextMenu, type PageMenuActions, pageMenuItems } from "./browserContextMenu";

function actions(): PageMenuActions {
  return { openInNewTab: vi.fn(), copy: vi.fn(), paste: vi.fn(), back: vi.fn(), forward: vi.fn(), reload: vi.fn() };
}

const nav = ["Back", "Forward", "Reload"];

describe("pageMenuItems", () => {
  it.each<[string, PageContextMenu, string[], string[]]>([
    ["plain page", {}, nav, []],
    ["a link", { link: "https://a.test/x" }, ["Open link in new tab", "Copy link address", ...nav], ["Back"]],
    ["an image", { image: "http://a.test/i.png" }, ["Open image in new tab", "Copy image address", ...nav], ["Back"]],
    ["a data: image, which new_tab refuses", { image: "data:image/png;base64,AA" }, ["Copy image address", ...nav], ["Back"]],
    ["selected text", { selection: "words" }, ["Copy", ...nav], ["Back"]],
    ["a text field", { editable: true }, ["Paste", ...nav], ["Back"]],
    [
      "a linked image with text selected in a field",
      { link: "https://a.test/x", image: "https://a.test/i.png", selection: "w", editable: true },
      ["Open link in new tab", "Copy link address", "Open image in new tab", "Copy image address", "Copy", "Paste", ...nav],
      ["Open image in new tab", "Copy", "Back"],
    ],
  ])("%s", (_, menu, labels, separated) => {
    const items = pageMenuItems(menu, actions());
    expect(items.map((i) => i.label)).toEqual(labels);
    expect(items.filter((i) => i.separator).map((i) => i.label)).toEqual(separated);
  });

  it("wires each item to its action", () => {
    const a = actions();
    const items = pageMenuItems({ link: "https://a.test/l", image: "https://a.test/i", selection: "sel", editable: true }, a);
    const click = (label: string) => items.find((i) => i.label === label)?.onClick();
    click("Open link in new tab");
    click("Copy link address");
    click("Open image in new tab");
    click("Copy image address");
    click("Copy");
    click("Paste");
    click("Back");
    click("Forward");
    click("Reload");
    expect(a.openInNewTab).toHaveBeenNthCalledWith(1, "https://a.test/l");
    expect(a.openInNewTab).toHaveBeenNthCalledWith(2, "https://a.test/i");
    expect(a.copy).toHaveBeenNthCalledWith(1, "https://a.test/l");
    expect(a.copy).toHaveBeenNthCalledWith(2, "https://a.test/i");
    expect(a.copy).toHaveBeenNthCalledWith(3, "sel");
    for (const fn of [a.paste, a.back, a.forward, a.reload]) expect(fn).toHaveBeenCalledTimes(1);
  });
});
