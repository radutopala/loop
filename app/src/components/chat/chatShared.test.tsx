import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { checkFilesExist } from "../../api/files";
import { darkColors, fonts, lightColors } from "../../theme";
import { resetFileLinkCache } from "../../utils/fileLinks";
import { buildActivityStyle, buildMessageStyles, FILE_PATH_TOOLS, renderInputWithLinks } from "./chatShared";

vi.mock("../../api/files", () => ({
  checkFilesExist: vi.fn(),
}));

const existsMock = vi.mocked(checkFilesExist);

// Every path the links ask about exists, under root 0 at the same path.
function allExist() {
  existsMock.mockImplementation(async (_channelId, paths) => paths.map((p) => ({ path: p, exists: true, root_index: 0, rel_path: p })));
}

function renderInput(input: string, toolName: string, channelId = "ch-1") {
  return render(<div data-testid="out">{renderInputWithLinks(input, toolName, channelId)}</div>);
}

describe("buildMessageStyles", () => {
  it("takes its colors from the palette", () => {
    const s = buildMessageStyles(darkColors);
    expect(s.loadMore?.border).toBe(`1px solid ${darkColors.border}`);
    expect(s.loadMore?.color).toBe(darkColors.textMuted);
    expect(s.time?.color).toBe(darkColors.textDim);
    expect(s.content?.color).toBe(darkColors.text);
    expect(s.codeBlock?.backgroundColor).toBe(darkColors.surface);
    expect(s.codeBlock?.color).toBe(darkColors.textLight);
    expect(s.codeBlock?.fontFamily).toBe(fonts.mono);
    expect(s.inlineCode?.backgroundColor).toBe(darkColors.surface);
    expect(s.blockquote?.borderLeft).toBe(`3px solid ${darkColors.border}`);
    expect(s.tableHeaderCell?.backgroundColor).toBe(darkColors.surface);
    expect(s.tableCell?.border).toBe(`1px solid ${darkColors.border}`);
  });

  it("caches styles per palette reference", () => {
    expect(buildMessageStyles(darkColors)).toBe(buildMessageStyles(darkColors));
    expect(buildMessageStyles(darkColors)).not.toBe(buildMessageStyles(lightColors));
  });
});

describe("buildActivityStyle", () => {
  it("renders activity dim and monospaced", () => {
    const style = buildActivityStyle(darkColors);
    expect(style.color).toBe(darkColors.textDim);
    expect(style.fontFamily).toBe(fonts.mono);
    expect(style.display).toBe("flex");
  });
});

describe("FILE_PATH_TOOLS", () => {
  it("lists the tools whose input is a bare path", () => {
    expect([...FILE_PATH_TOOLS].sort()).toEqual(["Edit", "MultiEdit", "NotebookEdit", "NotebookRead", "Read", "Write"]);
    expect(FILE_PATH_TOOLS.has("Bash")).toBe(false);
  });
});

describe("renderInputWithLinks", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetFileLinkCache();
    allExist();
  });

  it("returns an empty input as is", () => {
    expect(renderInputWithLinks("", "Read", "ch-1")).toBe("");
  });

  it("returns the input as text without a channel", () => {
    expect(renderInputWithLinks("src/app.ts", "Read", "")).toBe("src/app.ts");
  });

  it("links a file tool's whole input as one path", async () => {
    renderInput("docs/my notes.md", "Read");
    const link = await screen.findByRole("link", { name: "docs/my notes.md" });
    expect(link.getAttribute("href")).toBe("#");
    expect(existsMock).toHaveBeenCalledWith("ch-1", ["docs/my notes.md"]);
  });

  it("returns input without paths as is", () => {
    expect(renderInputWithLinks("echo hello", "Bash", "ch-1")).toBe("echo hello");
  });

  it("links each path inside other text, keeping the text around them", async () => {
    renderInput("grep foo src/a.ts and lib/b.go:12 now", "Grep");
    expect(await screen.findByRole("link", { name: "src/a.ts" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "lib/b.go:12" })).toBeTruthy();
    expect(screen.getByTestId("out").textContent).toBe("grep foo src/a.ts and lib/b.go:12 now");
    expect(existsMock).toHaveBeenCalledWith("ch-1", ["src/a.ts", "lib/b.go"]);
  });

  it("links a path that starts and ends the input", async () => {
    renderInput("src/a.ts", "Grep");
    expect(await screen.findByRole("link", { name: "src/a.ts" })).toBeTruthy();
    expect(screen.getByTestId("out").textContent).toBe("src/a.ts");
  });

  it("checks a relative path in a shell command against the dir it cd'd to", async () => {
    renderInput("cd /repo/app && cat src/a.ts", "Bash");
    expect(await screen.findByRole("link", { name: "src/a.ts" })).toBeTruthy();
    expect(existsMock).toHaveBeenCalledWith("ch-1", ["/repo/app/src/a.ts"]);
  });

  it("ignores a cd in input that isn't a shell command", async () => {
    renderInput("cd /repo/app && cat src/a.ts", "Grep");
    expect(await screen.findByRole("link", { name: "src/a.ts" })).toBeTruthy();
    expect(existsMock).toHaveBeenCalledWith("ch-1", ["src/a.ts"]);
  });

  it("shows a path that doesn't exist as plain text", async () => {
    existsMock.mockResolvedValue([{ path: "src/gone.ts", exists: false }]);
    renderInput("cat src/gone.ts", "Bash");
    await waitFor(() => expect(existsMock).toHaveBeenCalled());
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByTestId("out").textContent).toBe("cat src/gone.ts");
  });
});
