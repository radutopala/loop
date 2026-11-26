import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { checkFilesExist } from "../../api/files";
import { resetFileLinkCache } from "../../utils/fileLinks";
import { ChannelContext } from "./chatShared";
import { MarkdownContent } from "./markdown";

vi.mock("../../api/files", () => ({
  checkFilesExist: vi.fn(),
}));

const existsMock = vi.mocked(checkFilesExist);

// Renders content as a message in channel (no channel when it's empty), with
// user-event's clipboard stub in place so copies can be read back.
function renderMarkdown(content: string, channel = "ch-1") {
  const user = userEvent.setup();
  const view = render(
    <ChannelContext.Provider value={channel}>
      <div data-testid="msg">
        <MarkdownContent content={content} />
      </div>
    </ChannelContext.Provider>,
  );
  return { user, ...view };
}

const msg = () => screen.getByTestId("msg");

// opacity is how a CopyButton shows: 0 hidden, 1 shown.
const opacity = (el: HTMLElement) => el.style.opacity;

describe("MarkdownContent", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetFileLinkCache();
    existsMock.mockImplementation(async (_channelId, paths) => paths.map((p) => ({ path: p, exists: true, root_index: 0, rel_path: p })));
  });

  describe("lines", () => {
    it("renders each line as a paragraph and a blank line as a break", () => {
      renderMarkdown("first\n\nsecond");
      const ps = msg().querySelectorAll("p");
      expect([...ps].map((p) => p.textContent)).toEqual(["first", "second"]);
      expect(msg().querySelectorAll("br")).toHaveLength(1);
    });
  });

  describe("inline formatting", () => {
    it("renders code, bold and italic", () => {
      renderMarkdown("run `make test` **now** or *later*");
      expect(msg().querySelector("code")?.textContent).toBe("make test");
      expect(msg().querySelector("strong")?.textContent).toBe("now");
      expect(msg().querySelector("em")?.textContent).toBe("later");
      expect(msg().querySelector("p")?.textContent).toBe("run make test now or later");
    });

    it("formats inside bold and italic", () => {
      renderMarkdown("**see https://example.test/a** and *`x`*");
      const link = within(msg().querySelector("strong")!).getByRole("link", { name: "https://example.test/a" });
      expect(link.getAttribute("href")).toBe("https://example.test/a");
      expect(msg().querySelector("em code")?.textContent).toBe("x");
    });

    it("renders inline math in both delimiters", () => {
      renderMarkdown("area $x^2$ and \\(y\\)");
      expect(msg().querySelectorAll(".katex")).toHaveLength(2);
      expect(msg().textContent).not.toContain("$");
    });

    it("opens a bare URL in a new window and leaves trailing punctuation as text", () => {
      renderMarkdown("go to https://example.test/x.");
      const link = screen.getByRole("link", { name: "https://example.test/x" });
      expect(link.getAttribute("href")).toBe("https://example.test/x");
      expect(link.getAttribute("target")).toBe("_blank");
      expect(link.getAttribute("rel")).toBe("noopener noreferrer");
      expect(link.getAttribute("data-copy-href")).toBe("https://example.test/x");
      expect(msg().querySelector("p")?.textContent).toBe("go to https://example.test/x.");
    });

    it("links a loop:// channel URL inside the app", () => {
      renderMarkdown("loop://channel/abc/42");
      const link = screen.getByRole("link", { name: "loop://channel/abc/42" });
      expect(link.getAttribute("href")).toBe("#abc/42");
      expect(link.getAttribute("target")).toBeNull();
      expect(link.getAttribute("data-copy-href")).toBe("loop://channel/abc/42");
    });

    it("shows a link's copy button on hover and copies its address", async () => {
      const { user } = renderMarkdown("https://example.test/y");
      const button = screen.getByTestId("link-copy");
      expect(opacity(button)).toBe("0");
      await user.hover(screen.getByRole("link"));
      expect(opacity(button)).toBe("1");
      // user-event checks pointer-events before the hover lands, so click directly.
      fireEvent.click(button);
      await waitFor(async () => expect(await navigator.clipboard.readText()).toBe("https://example.test/y"));
      await user.unhover(screen.getByRole("link"));
      expect(button.style.pointerEvents).toBe("none");
    });

    it("renders a markdown link to a web page with its label", () => {
      renderMarkdown("read [the docs](https://example.test/docs) first");
      const link = screen.getByRole("link", { name: "the docs" });
      expect(link.getAttribute("href")).toBe("https://example.test/docs");
      expect(link.getAttribute("target")).toBe("_blank");
    });

    it("opens a markdown link to a file in the editor", async () => {
      renderMarkdown("see [the config](src/config.ts#L3)");
      const link = await screen.findByRole("link", { name: "the config" });
      expect(link.getAttribute("href")).toBe("#");
      expect(existsMock).toHaveBeenCalledWith("ch-1", ["src/config.ts"]);
    });

    it("keeps a markdown link to a file a plain link without a channel", () => {
      renderMarkdown("see [the config](src/config.ts)", "");
      const link = screen.getByRole("link", { name: "the config" });
      expect(link.getAttribute("href")).toBe("src/config.ts");
      expect(existsMock).not.toHaveBeenCalled();
    });

    it("links file paths in the text, keeping the text around them", async () => {
      renderMarkdown("edit src/a.ts:4 then `x` lib/b.go");
      expect(await screen.findByRole("link", { name: "src/a.ts:4" })).toBeTruthy();
      expect(screen.getByRole("link", { name: "lib/b.go" })).toBeTruthy();
      expect(msg().querySelector("p")?.textContent).toBe("edit src/a.ts:4 then x lib/b.go");
    });

    it("links a code span that names a file, keeping it code", async () => {
      renderMarkdown("see `docker-compose.yml`, `src/a.ts:12:3` and `make test`");
      const bare = await screen.findByRole("link", { name: "docker-compose.yml" });
      expect(bare.closest("code")).toBeTruthy();
      expect(screen.getByRole("link", { name: "src/a.ts:12:3" }).closest("code")).toBeTruthy();
      expect(screen.queryByRole("link", { name: "make test" })).toBeNull();
      expect(existsMock).toHaveBeenCalledWith("ch-1", ["docker-compose.yml", "src/a.ts"]);
    });

    it("keeps a code span naming a missing file plain code", async () => {
      existsMock.mockImplementation(async (_channelId, paths) => paths.map((p) => ({ path: p, exists: false })));
      renderMarkdown("see `notes.md`");
      await waitFor(() => expect(existsMock).toHaveBeenCalledWith("ch-1", ["notes.md"]));
      expect(screen.queryByRole("link")).toBeNull();
      expect(msg().querySelector("code")?.textContent).toBe("notes.md");
    });

    it("leaves a code span naming a file as code without a channel", () => {
      renderMarkdown("see `notes.md`", "");
      expect(screen.queryByRole("link")).toBeNull();
      expect(existsMock).not.toHaveBeenCalled();
    });

    it("links a path that is the whole line", async () => {
      renderMarkdown("src/a.ts");
      expect(await screen.findByRole("link", { name: "src/a.ts" })).toBeTruthy();
    });

    it("leaves paths as text without a channel", () => {
      renderMarkdown("edit src/a.ts", "");
      expect(screen.queryByRole("link")).toBeNull();
      expect(msg().querySelector("p")?.textContent).toBe("edit src/a.ts");
      expect(existsMock).not.toHaveBeenCalled();
    });
  });

  describe("code blocks", () => {
    it("renders a fenced block with its language and copies its text and fence", async () => {
      const { user } = renderMarkdown("```go\nfmt.Println(1)\n```\nafter");
      const block = screen.getByTestId("code-block");
      expect(block.querySelector("code")?.textContent).toBe("fmt.Println(1)");
      expect(block.querySelector("pre div")?.textContent).toBe("go");
      expect(msg().querySelector("p")?.textContent).toBe("after");

      const asText = within(block).getByRole("button", { name: "Copy as text" });
      const asFence = within(block).getByRole("button", { name: "Copy as code block" });
      expect(opacity(asText)).toBe("0");
      await user.hover(block);
      expect(opacity(asText)).toBe("1");
      await user.click(asText);
      expect(await navigator.clipboard.readText()).toBe("fmt.Println(1)");
      await user.click(asFence);
      expect(await navigator.clipboard.readText()).toBe("```go\nfmt.Println(1)\n```");
      await user.unhover(block);
      expect(opacity(asFence)).toBe("1"); // still showing its check
    });

    it("renders a block without a language and an unclosed fence to the end", () => {
      renderMarkdown("```\nline 1\nline 2");
      const block = screen.getByTestId("code-block");
      expect(block.querySelector("code")?.textContent).toBe("line 1\nline 2");
      expect(block.querySelector("pre div")).toBeNull();
    });

    it("renders a closed component fence as a chat component", () => {
      renderMarkdown("```loop-component chart Sales\n<p>hi</p>\n```");
      const component = screen.getByTestId("chat-component");
      expect(component.getAttribute("data-template")).toBe("chart");
      expect(screen.getByTestId("chat-component-frame").getAttribute("srcdoc")).toBe("<p>hi</p>");
      expect(screen.queryByTestId("code-block")).toBeNull();
    });

    it("renders an unclosed component fence as code", () => {
      renderMarkdown("```loop-component chart\n<p>hi</p>");
      expect(screen.queryByTestId("chat-component")).toBeNull();
      expect(screen.getByTestId("code-block").querySelector("code")?.textContent).toBe("<p>hi</p>");
    });
  });

  describe("display math", () => {
    it("renders a display formula and copies its LaTeX", async () => {
      const { user } = renderMarkdown("$$\nx = 1\n$$");
      const display = screen.getByTestId("math-display");
      expect(display.querySelector(".katex-display")).toBeTruthy();
      const copy = within(display).getByRole("button", { name: "Copy as LaTeX" });
      expect(opacity(copy)).toBe("0");
      await user.hover(display);
      expect(opacity(copy)).toBe("1");
      await user.click(copy);
      expect(await navigator.clipboard.readText()).toBe("$$\nx = 1\n$$");
      await user.unhover(display);
    });
  });

  describe("tables", () => {
    const source = "| Name | Qty | Note |\n|:---|---:|\n| [a](https://example.test/a) | 2 | x | extra |";

    it("renders headers and rows with their alignments, left by default", () => {
      renderMarkdown(`${source}\nafter`);
      const table = screen.getByRole("table");
      const ths = within(table).getAllByRole("columnheader");
      expect(ths.map((th) => th.textContent)).toEqual(["Name", "Qty", "Note"]);
      expect(ths.map((th) => th.style.textAlign)).toEqual(["left", "right", "left"]);
      const tds = within(table).getAllByRole("cell");
      expect(tds.map((td) => td.textContent)).toEqual(["a", "2", "x", "extra"]);
      expect(tds.map((td) => td.style.textAlign)).toEqual(["left", "right", "left", "left"]);
      expect(within(table).getByRole("link", { name: "a" }).getAttribute("href")).toBe("https://example.test/a");
      expect(msg().querySelector(":scope > p")?.textContent).toBe("after");
    });

    it("copies the table as markdown, as a table and as HTML markup", async () => {
      const { user } = renderMarkdown(source);
      const wrapper = screen.getByRole("table").parentElement!;
      const asMarkdown = screen.getByRole("button", { name: "Copy as markdown" });
      expect(opacity(asMarkdown)).toBe("0");
      await user.hover(wrapper);
      expect(opacity(asMarkdown)).toBe("1");

      await user.click(asMarkdown);
      expect(await navigator.clipboard.readText()).toBe(source);
      await user.click(screen.getByRole("button", { name: "Copy as table (Slack, Teams, Sheets, docs)" }));
      expect(await navigator.clipboard.readText()).toBe("Name\tQty\tNote\na (https://example.test/a)\t2\tx\textra");
      await user.click(screen.getByRole("button", { name: "Copy as HTML markup" }));
      const markup = await navigator.clipboard.readText();
      expect(markup).toContain("<table>");
      expect(markup).toContain('<a href="https://example.test/a">a</a>');
      await user.unhover(wrapper);
    });
  });

  describe("blockquotes", () => {
    it("collects consecutive quoted lines, a bare > as an empty line", () => {
      renderMarkdown("> first **bold**\n>\n> second\nafter");
      const quote = screen.getByTestId("block-quote");
      const ps = quote.querySelectorAll("blockquote p");
      expect([...ps].map((p) => p.textContent)).toEqual(["first bold", "", "second"]);
      expect(ps[1]?.querySelector("br")).toBeTruthy();
      expect(quote.querySelector("strong")?.textContent).toBe("bold");
      expect(msg().querySelector(":scope > p")?.textContent).toBe("after");
    });

    it("copies the quote as text and as markdown", async () => {
      const { user } = renderMarkdown("> first\n>\n> second");
      const quote = screen.getByTestId("block-quote");
      const asText = within(quote).getByRole("button", { name: "Copy as text" });
      expect(opacity(asText)).toBe("0");
      await user.hover(quote);
      expect(opacity(asText)).toBe("1");
      await user.click(asText);
      expect(await navigator.clipboard.readText()).toBe("first\n\nsecond");
      await user.click(within(quote).getByRole("button", { name: "Copy as quote" }));
      expect(await navigator.clipboard.readText()).toBe("> first\n>\n> second");
      await user.unhover(quote);
    });
  });
});
