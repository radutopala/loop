import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { checkFilesExist } from "../../api/files";
import { resetFileLinkCache } from "../../utils/fileLinks";
import { FileLink, type FileLinkOpenDetail } from "./FileLink";

vi.mock("../../api/files", () => ({ checkFilesExist: vi.fn() }));

const checkMock = vi.mocked(checkFilesExist);

describe("FileLink", () => {
  beforeEach(() => {
    resetFileLinkCache();
    checkMock.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("stays plain text for a path that isn't found", async () => {
    checkMock.mockResolvedValue([{ path: "nope.go", exists: false }]);
    render(<FileLink channelId="ch-1" raw="nope.go" line={null} />);
    expect(screen.getByText("nope.go").tagName).toBe("SPAN");
    await waitFor(() => expect(checkMock).toHaveBeenCalledWith("ch-1", ["nope.go"]));
    expect(screen.getByText("nope.go").tagName).toBe("SPAN");
  });

  it("becomes a link once the path is found, and opens it at its line", async () => {
    checkMock.mockResolvedValue([{ path: "src/a.go", exists: true, rel_path: "src/a.go", root_index: 1 }]);
    const opened = vi.fn();
    window.addEventListener("loop:open-file", opened);
    render(<FileLink channelId="ch-1" raw="src/a.go" line={12} />);
    const link = await screen.findByRole("link", { name: "src/a.go:12" });
    await userEvent.click(link);
    window.removeEventListener("loop:open-file", opened);
    const detail = (opened.mock.calls[0]![0] as CustomEvent<FileLinkOpenDetail>).detail;
    expect(detail).toEqual({ channelId: "ch-1", target: { rootIndex: 1, relPath: "src/a.go" }, line: 12 });
  });

  it("checks checkPath but reads its label", async () => {
    checkMock.mockResolvedValue([{ path: "/w/sub/a.go", exists: true, rel_path: "sub/a.go", root_index: 0 }]);
    render(<FileLink channelId="ch-1" raw="a.go" line={3} checkPath="/w/sub/a.go" label="the file" />);
    expect(await screen.findByRole("link", { name: "the file" })).toBeTruthy();
    expect(checkMock).toHaveBeenCalledWith("ch-1", ["/w/sub/a.go"]);
  });

  it("renders a path already found as a link at once and stops listening on unmount", async () => {
    checkMock.mockResolvedValue([{ path: "b.go", exists: true, rel_path: "b.go", root_index: 0 }]);
    const first = render(<FileLink channelId="ch-1" raw="b.go" line={null} />);
    await screen.findByRole("link", { name: "b.go" });
    first.unmount();
    render(<FileLink channelId="ch-1" raw="b.go" line={null} />);
    expect(screen.getByRole("link", { name: "b.go" })).toBeTruthy();
    expect(checkMock).toHaveBeenCalledOnce();
  });
});
