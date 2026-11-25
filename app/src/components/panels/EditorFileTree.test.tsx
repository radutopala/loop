import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FileEntry } from "../../api/loopApi";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors } from "../../theme";
import { FileIcon, FileTree, type FileTreeProps, loadTreeWidth, makePathKey, parsePathKey, saveTreeWidth, TREE_DEFAULT_WIDTH, TREE_MAX_WIDTH, TREE_MIN_WIDTH } from "./EditorFileTree";

const rgb = (color: string) => {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
};

describe("path keys", () => {
  it("prefixes a path with its root and parses it back", () => {
    expect(makePathKey(2, "src/a.ts")).toBe("2:src/a.ts");
    expect(parsePathKey("2:src/a.ts")).toEqual({ rootIndex: 2, relativePath: "src/a.ts" });
  });

  it.each([
    ["a key without a root", "src/a.ts", { rootIndex: 0, relativePath: "src/a.ts" }],
    ["a root that isn't a number", "x:a.ts", { rootIndex: 0, relativePath: "a.ts" }],
    ["a root's own key", "1:", { rootIndex: 1, relativePath: "" }],
  ])("parses %s", (_name, key, want) => {
    expect(parsePathKey(key)).toEqual(want);
  });
});

describe("tree width", () => {
  beforeEach(() => localStorage.clear());

  it("saves the width and loads it back", () => {
    saveTreeWidth(300);
    expect(localStorage.getItem("loop-editor-tree-width")).toBe("300");
    expect(loadTreeWidth()).toBe(300);
  });

  it.each([
    ["nothing stored", null],
    ["too narrow", String(TREE_MIN_WIDTH - 1)],
    ["too wide", String(TREE_MAX_WIDTH + 1)],
    ["not a number", "wide"],
  ])("falls back to the default with %s", (_name, stored) => {
    if (stored !== null) localStorage.setItem("loop-editor-tree-width", stored);
    expect(loadTreeWidth()).toBe(TREE_DEFAULT_WIDTH);
  });
});

describe("FileIcon", () => {
  const icon = (name: string) => {
    const { container, unmount } = render(
      <ThemeProvider initialTheme="dark">
        <FileIcon name={name} />
      </ThemeProvider>,
    );
    const out = { stroke: container.querySelector("path")?.getAttribute("stroke"), label: container.querySelector("text")?.textContent ?? null };
    unmount();
    return out;
  };

  it.each([
    ["main.go", "#00add8", "Go"],
    ["a.ts", "#3178c6", "TS"],
    ["a.TSX", "#3178c6", "TS"],
    ["a.js", "#f7df1e", "JS"],
    ["a.jsx", "#f7df1e", "JS"],
    ["a.mjs", "#f7df1e", "JS"],
    ["a.cjs", "#f7df1e", "JS"],
    ["a.py", "#3776ab", "Py"],
    ["a.rs", "#dea584", "Rs"],
    ["a.json", "#cbcb41", "{}"],
    ["a.jsonl", "#cbcb41", "{}"],
    ["a.yaml", "#cb171e", "Y"],
    ["a.yml", "#cb171e", "Y"],
    ["a.toml", "#9c4221", "T"],
    ["a.md", "#519aba", "M"],
    ["a.mdx", "#519aba", "M"],
    ["a.html", "#e34c26", "<>"],
    ["a.htm", "#e34c26", "<>"],
    ["a.css", "#563d7c", "#"],
    ["a.scss", "#563d7c", "#"],
    ["a.less", "#563d7c", "#"],
    ["a.svg", "#ffb13b", "S"],
    ["a.sh", "#89e051", "$"],
    ["a.bash", "#89e051", "$"],
    ["a.zsh", "#89e051", "$"],
    ["a.sql", "#e38c00", "Q"],
    ["go.mod", "#00add8", "Go"],
    ["go.sum", "#00add8", "Go"],
    ["Dockerfile", "#384d54", "D"],
    ["Makefile", "#6d8086", "M"],
    ["LICENSE", "#d4930d", "L"],
    [".gitignore", "#f14e32", "G"],
    [".env", "#ecd53f", "E"],
    ["a.png", "#a074c4", "I"],
    ["a.jpg", "#a074c4", "I"],
    ["a.jpeg", "#a074c4", "I"],
    ["a.gif", "#a074c4", "I"],
    ["a.webp", "#a074c4", "I"],
    ["a.ico", "#a074c4", "I"],
  ])("draws %s in %s with label %s", (name, color, label) => {
    expect(icon(name)).toEqual({ stroke: color, label });
  });

  it.each(["a.txt", "a.log", "a.out"])("draws %s in its colour without a label", (name) => {
    expect(icon(name)).toEqual({ stroke: "#6d8086", label: null });
  });

  it.each(["notes", "a.weird", "trailing."])("draws an unknown file %s in the dim text colour", (name) => {
    expect(icon(name)).toEqual({ stroke: darkColors.textDim, label: null });
  });
});

describe("FileTree", () => {
  const entries: FileEntry[] = [
    { name: "src", type: "dir" },
    { name: "main.go", type: "file" },
  ];
  const nested = new Map<string, FileEntry[]>([["0:src", [{ name: "a.ts", type: "file" }]]]);

  const renderTree = (overrides: Partial<FileTreeProps> = {}) => {
    const props: FileTreeProps = {
      entries,
      dirContents: nested,
      expandedDirs: new Set(),
      selectedPath: null,
      previewTab: null,
      selectedDir: "",
      depth: 1,
      parentPath: "0:",
      rootIndex: 0,
      onDirClick: vi.fn(),
      onFileClick: vi.fn(),
      onFileDoubleClick: vi.fn(),
      onContextMenu: vi.fn(),
      ...overrides,
    };
    render(
      <ThemeProvider initialTheme="dark">
        <FileTree {...props} />
      </ThemeProvider>,
    );
    return props;
  };
  const row = (path: string) => document.querySelector<HTMLElement>(`[data-testid="file-tree-entry"][data-path="${path}"]`)!;

  afterEach(() => vi.restoreAllMocks());

  it("lists the entries with their root-prefixed paths and marks directories", () => {
    renderTree();
    const rows = screen.getAllByTestId("file-tree-entry");
    expect(rows.map((r) => [r.dataset.path, r.dataset.dir, r.textContent])).toEqual([
      ["0:src", "true", "src"],
      ["0:main.go", undefined, "Gomain.go"],
    ]);
    expect(rows[0]!.style.padding).toBe("3px 8px 3px 24px");
  });

  it("shows an expanded directory's children one level deeper", () => {
    renderTree({ expandedDirs: new Set(["0:src"]) });
    expect(row("0:src/a.ts").style.padding).toBe("3px 8px 3px 40px");
    expect(row("0:src").querySelector("svg")?.style.transform).toBe("rotate(90deg)");
  });

  it("shows no children for an expanded directory not loaded yet", () => {
    renderTree({ expandedDirs: new Set(["0:src"]), dirContents: new Map() });
    expect(screen.getAllByTestId("file-tree-entry")).toHaveLength(2);
  });

  it("builds children's paths under a nested parent in another root", () => {
    renderTree({ parentPath: "1:lib", rootIndex: 1 });
    expect(row("1:lib/src")).toBeTruthy();
  });

  it("opens a directory on click and a file on click and double-click", () => {
    const props = renderTree();
    fireEvent.click(row("0:src"));
    fireEvent.doubleClick(row("0:src"));
    fireEvent.click(row("0:main.go"));
    fireEvent.doubleClick(row("0:main.go"));
    expect(props.onDirClick).toHaveBeenCalledExactlyOnceWith("0:src");
    expect(props.onFileClick).toHaveBeenCalledExactlyOnceWith("0:main.go", entries[1]);
    expect(props.onFileDoubleClick).toHaveBeenCalledExactlyOnceWith("0:main.go", entries[1]);
  });

  it("passes context menu events with the path and whether it's a directory", () => {
    const props = renderTree();
    fireEvent.contextMenu(row("0:src"));
    fireEvent.contextMenu(row("0:main.go"));
    expect(vi.mocked(props.onContextMenu).mock.calls.map(([, path, isDir]) => [path, isDir])).toEqual([
      ["0:src", true],
      ["0:main.go", false],
    ]);
  });

  it("highlights the selected file and directory and leaves them on hover", () => {
    renderTree({ selectedPath: "0:main.go", selectedDir: "0:src" });
    expect(row("0:main.go").style.color).toBe(rgb(darkColors.textLight));
    expect(row("0:src").style.color).toBe(rgb(darkColors.textLight));
    for (const path of ["0:main.go", "0:src"]) {
      const before = row(path).style.backgroundColor;
      fireEvent.mouseEnter(row(path));
      fireEvent.mouseLeave(row(path));
      expect(row(path).style.backgroundColor).toBe(before);
    }
  });

  it("highlights an unselected row on hover", () => {
    renderTree();
    fireEvent.mouseEnter(row("0:main.go"));
    const hover = document.createElement("div");
    hover.style.backgroundColor = darkColors.hoverBg;
    expect(row("0:main.go").style.backgroundColor).toBe(hover.style.backgroundColor);
    fireEvent.mouseLeave(row("0:main.go"));
    expect(row("0:main.go").style.backgroundColor).toBe("transparent");
    expect(row("0:main.go").style.color).toBe(rgb(darkColors.text));
  });
});
