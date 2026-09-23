import { describe, expect, it } from "vitest";
import { escapeHTML, inlineToHTML, isTableSeparator, parseTableAligns, parseTableBlock, plainCell, splitTableRow, startsTable, tableToHTML, tableToTSV } from "./markdownTable";

describe("startsTable", () => {
  const cases: { name: string; lines: string[]; want: boolean }[] = [
    { name: "header plus separator", lines: ["| PR | What |", "|----|------|"], want: true },
    { name: "pipeless header", lines: ["PR | What", "---|---"], want: true },
    { name: "separator missing", lines: ["| PR | What |", "| #31 | thing |"], want: false },
    { name: "header is the last line", lines: ["| PR | What |"], want: false },
    { name: "fenced code that contains a pipe", lines: ["```sh", "a | b"], want: false },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(startsTable(c.lines, 0)).toBe(c.want);
    });
  }
});

describe("isTableSeparator", () => {
  const cases: { line: string; want: boolean }[] = [
    { line: "|---|---|", want: true },
    { line: "| :--- | ---: | :---: |", want: true },
    { line: "---", want: false }, // a horizontal rule, only one column
    { line: "| a | b |", want: false },
  ];
  for (const c of cases) {
    it(`${c.line} -> ${c.want}`, () => {
      expect(isTableSeparator(c.line)).toBe(c.want);
    });
  }
});

describe("parseTableAligns", () => {
  it("reads the colons", () => {
    expect(parseTableAligns("| :--- | ---: | :---: | --- |")).toEqual(["left", "right", "center", "left"]);
  });
});

describe("splitTableRow", () => {
  it("trims the outer pipes and each cell", () => {
    expect(splitTableRow("|  a |b  | c |")).toEqual(["a", "b", "c"]);
  });

  it("keeps cells when the outer pipes are absent", () => {
    expect(splitTableRow("a | b")).toEqual(["a", "b"]);
  });

  it("keeps escaped pipes inside a cell", () => {
    expect(splitTableRow("| \\|3 − 2x\\| | 3 − 2x ≥ 5 |")).toEqual(["|3 − 2x|", "3 − 2x ≥ 5"]);
  });

  it("keeps an escaped pipe that ends the row", () => {
    expect(splitTableRow("a | b \\|")).toEqual(["a", "b |"]);
  });
});

describe("parseTableBlock", () => {
  const lines = ["Open PRs:", "| PR | Ticket |", "|----|--------|", "| #31 | ABC-1 |", "| #32 | ABC-2 |", "", "trailing text"];

  it("parses headers, aligns and rows", () => {
    const table = parseTableBlock(lines, 1);
    expect(table.headers).toEqual(["PR", "Ticket"]);
    expect(table.aligns).toEqual(["left", "left"]);
    expect(table.rows).toEqual([
      ["#31", "ABC-1"],
      ["#32", "ABC-2"],
    ]);
  });

  it("keeps the original markdown as the copyable source", () => {
    expect(parseTableBlock(lines, 1).source).toBe("| PR | Ticket |\n|----|--------|\n| #31 | ABC-1 |\n| #32 | ABC-2 |");
  });

  it("stops at the first non-table line", () => {
    expect(parseTableBlock(lines, 1).next).toBe(5);
  });

  it("handles a header-only table", () => {
    const table = parseTableBlock(["| PR |", "|----|"], 0);
    expect(table.rows).toEqual([]);
    expect(table.source).toBe("| PR |\n|----|");
    expect(table.next).toBe(2);
  });

  it("keeps ragged rows as written", () => {
    const table = parseTableBlock(["| a | b |", "|---|---|", "| 1 |"], 0);
    expect(table.rows).toEqual([["1"]]);
    expect(table.source).toBe("| a | b |\n|---|---|\n| 1 |");
  });
});

describe("plainCell", () => {
  const cases: { name: string; in: string; want: string }[] = [
    { name: "link keeps its label and its url", in: "[#31](https://example.test/pull/31)", want: "#31 (https://example.test/pull/31)" },
    { name: "self-linked url is not doubled", in: "[https://example.test/x](https://example.test/x)", want: "https://example.test/x" },
    { name: "code loses its backticks", in: "`retry_count` on the job", want: "retry_count on the job" },
    { name: "bold loses its stars", in: "**done**", want: "done" },
    { name: "italic loses its star", in: "*maybe*", want: "maybe" },
    { name: "plain text is untouched", in: "plain words, nothing to strip", want: "plain words, nothing to strip" },
    { name: "bare url stays whole", in: "see https://example.test/x", want: "see https://example.test/x" },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(plainCell(c.in)).toBe(c.want);
    });
  }
});

describe("escapeHTML", () => {
  it("escapes the markup characters", () => {
    expect(escapeHTML('a & b < c > d "e"')).toBe("a &amp; b &lt; c &gt; d &quot;e&quot;");
  });
});

describe("inlineToHTML", () => {
  const cases: { name: string; in: string; want: string }[] = [
    { name: "markdown link", in: "[#31](https://example.test/pull/31)", want: '<a href="https://example.test/pull/31">#31</a>' },
    { name: "bare url", in: "see https://example.test/x", want: 'see <a href="https://example.test/x">https://example.test/x</a>' },
    { name: "code span", in: "`retry_count` on the job", want: "<code>retry_count</code> on the job" },
    { name: "bold", in: "**done**", want: "<strong>done</strong>" },
    { name: "italic", in: "*maybe*", want: "<em>maybe</em>" },
    { name: "plain text is escaped", in: "a < b & c", want: "a &lt; b &amp; c" },
    { name: "cell text around a token", in: "before `code` after", want: "before <code>code</code> after" },
    { name: "empty cell", in: "", want: "" },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(inlineToHTML(c.in)).toBe(c.want);
    });
  }
});

describe("tableToHTML", () => {
  it("renders a head and a body", () => {
    const out = tableToHTML({
      headers: ["PR", "What"],
      aligns: ["left", "left"],
      rows: [["[#31](https://example.test/pull/31)", "Retry the batch"]],
    });
    expect(out.split("\n")).toEqual([
      "<table>",
      "<thead><tr><th>PR</th><th>What</th></tr></thead>",
      '<tbody><tr><td><a href="https://example.test/pull/31">#31</a></td><td>Retry the batch</td></tr></tbody>',
      "</table>",
    ]);
  });

  it("carries non-default alignment as an attribute", () => {
    const out = tableToHTML({ headers: ["Count", "Mid"], aligns: ["right", "center"], rows: [["7", "ab"]] });
    expect(out).toContain('<th align="right">Count</th><th align="center">Mid</th>');
    expect(out).toContain('<td align="right">7</td><td align="center">ab</td>');
  });

  it("omits the body of a header-only table", () => {
    const out = tableToHTML({ headers: ["A"], aligns: ["left"], rows: [] });
    expect(out).toBe(["<table>", "<thead><tr><th>A</th></tr></thead>", "</table>"].join("\n"));
  });
});

describe("tableToTSV", () => {
  it("joins cells with tabs and rows with newlines", () => {
    const out = tableToTSV({
      headers: ["PR", "Ticket", "What"],
      rows: [
        ["[#31](https://example.test/pull/31)", "ABC-1007", "Retry the failed batch"],
        ["[#36](https://example.test/pull/36)", "ABC-1020", "`retry_count` on the job"],
      ],
    });
    expect(out.split("\n")).toEqual([
      "PR\tTicket\tWhat",
      "#31 (https://example.test/pull/31)\tABC-1007\tRetry the failed batch",
      "#36 (https://example.test/pull/36)\tABC-1020\tretry_count on the job",
    ]);
  });

  it("keeps a short row short", () => {
    expect(tableToTSV({ headers: ["A", "B"], rows: [["1"]] }).split("\n")).toEqual(["A\tB", "1"]);
  });
});
