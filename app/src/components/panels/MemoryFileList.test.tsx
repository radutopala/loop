import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MemoryFileList, type MemoryFileListProps, type TreeNode } from "./MemoryFileList";

const tree: TreeNode[] = [
  {
    name: "project",
    key: "project",
    isDir: true,
    children: [
      { name: "notes.md", key: "project/notes.md", fullPath: "/mem/project/notes.md", isDir: false, children: [] },
      { name: "sub", key: "project/sub", isDir: true, children: [{ name: "deep.md", key: "project/sub/deep.md", fullPath: "/mem/project/sub/deep.md", isDir: false, children: [] }] },
      { name: "orphan.md", key: "project/orphan.md", isDir: false, children: [] },
    ],
  },
];

function renderList(overrides: Partial<MemoryFileListProps> = {}) {
  const props: MemoryFileListProps = {
    tree,
    treeWidth: 200,
    treeMinWidth: 100,
    treeMaxWidth: 400,
    treeResizing: false,
    expandedDirs: new Set(["project"]),
    selectedPath: null,
    onLoadFiles: vi.fn(),
    onTreeResize: vi.fn(),
    onDirToggle: vi.fn(),
    onFileClick: vi.fn(),
    onFileDoubleClick: vi.fn(),
    ...overrides,
  };
  const utils = render(<MemoryFileList {...props} />);
  return { props, ...utils };
}

function resizeHandle(container: HTMLElement): HTMLDivElement {
  return container.lastElementChild as HTMLDivElement;
}

describe("MemoryFileList", () => {
  it("renders expanded directories and hides collapsed ones", () => {
    renderList();
    expect(screen.getByTitle("project").style.fontWeight).toBe("700");
    expect(screen.getByTitle("sub").style.fontWeight).toBe("400");
    expect(screen.getByTitle("/mem/project/notes.md")).toBeTruthy();
    expect(screen.queryByText("deep.md")).toBeNull();
    const chevron = screen.getByTitle("project").querySelector("svg") as SVGElement;
    expect(chevron.style.transform).toBe("rotate(90deg)");
  });

  it("shows nested files of an expanded subdirectory, indented", () => {
    renderList({ expandedDirs: new Set(["project", "project/sub"]) });
    expect(screen.getByTitle("/mem/project/sub/deep.md").style.padding).toBe("3px 8px 3px 40px");
  });

  it("toggles a directory and refreshes the list", () => {
    const { props } = renderList({ expandedDirs: new Set() });
    expect(screen.queryByText("notes.md")).toBeNull();
    fireEvent.click(screen.getByTitle("project"));
    expect(props.onDirToggle).toHaveBeenCalledWith("project");
    fireEvent.click(screen.getByTitle("Refresh files"));
    expect(props.onLoadFiles).toHaveBeenCalled();
  });

  it("reports clicks and double clicks on a file", () => {
    const { props } = renderList();
    const file = screen.getByTitle("/mem/project/notes.md");
    fireEvent.click(file);
    fireEvent.doubleClick(file);
    expect(props.onFileClick).toHaveBeenCalledWith("/mem/project/notes.md");
    expect(props.onFileDoubleClick).toHaveBeenCalledWith("/mem/project/notes.md");
  });

  it("ignores clicks on a file without a path", () => {
    const { props } = renderList();
    const orphan = screen.getByText("orphan.md");
    fireEvent.click(orphan);
    fireEvent.doubleClick(orphan);
    expect(props.onFileClick).not.toHaveBeenCalled();
    expect(props.onFileDoubleClick).not.toHaveBeenCalled();
  });

  it("highlights hover on unselected rows only", () => {
    renderList({ selectedPath: "/mem/project/notes.md" });
    const selected = screen.getByTitle("/mem/project/notes.md");
    const selectedBg = selected.style.background;
    expect(selectedBg).not.toBe("none");
    fireEvent.mouseEnter(selected);
    fireEvent.mouseLeave(selected);
    expect(selected.style.backgroundColor).not.toBe("transparent");

    const other = screen.getByText("orphan.md");
    expect(other.style.background).toBe("none");
    fireEvent.mouseEnter(other);
    expect(other.style.backgroundColor).not.toBe("transparent");
    expect(other.style.backgroundColor).not.toBe("");
    fireEvent.mouseLeave(other);
    expect(other.style.backgroundColor).toBe("transparent");

    const dir = screen.getByTitle("project");
    fireEvent.mouseEnter(dir);
    expect(dir.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(dir);
    expect(dir.style.backgroundColor).toBe("transparent");

    const refresh = screen.getByTitle("Refresh files");
    const dim = refresh.style.color;
    fireEvent.mouseEnter(refresh);
    expect(refresh.style.color).not.toBe(dim);
    fireEvent.mouseLeave(refresh);
    expect(refresh.style.color).toBe(dim);
  });

  it("starts a resize from the handle and keeps it lit while resizing", () => {
    const { props, container, rerender } = renderList();
    const handle = resizeHandle(container);
    expect(handle.style.backgroundColor).toBe("transparent");
    fireEvent.mouseDown(handle);
    expect(props.onTreeResize).toHaveBeenCalled();
    fireEvent.mouseEnter(handle);
    const lit = handle.style.backgroundColor;
    expect(lit).not.toBe("transparent");
    fireEvent.mouseLeave(handle);
    expect(handle.style.backgroundColor).toBe("transparent");

    rerender(<MemoryFileList {...props} treeResizing />);
    expect(handle.style.backgroundColor).toBe(lit);
    fireEvent.mouseEnter(handle);
    fireEvent.mouseLeave(handle);
    expect(handle.style.backgroundColor).toBe(lit);
  });
});
