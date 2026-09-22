export type TableAlign = "left" | "center" | "right";

/** A parsed GFM table, plus the markdown source it was parsed from. */
export type TableBlock = {
  headers: string[];
  aligns: TableAlign[];
  rows: string[][];
  /** The original markdown lines, so a copy of the table stays a table. */
  source: string;
  /** Index of the first line after the table. */
  next: number;
};

export function isTableRow(line: string): boolean {
  return line.includes("|") && line.trim().length > 0 && !line.trim().startsWith("```");
}

export function isTableSeparator(line: string): boolean {
  // |---|:---:|---:| with optional surrounding pipes/whitespace.
  return /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(line);
}

export function parseTableAligns(separator: string): TableAlign[] {
  return splitTableRow(separator).map((cell) => {
    const t = cell.trim();
    const left = t.startsWith(":");
    const right = t.endsWith(":");
    if (left && right) return "center";
    if (right) return "right";
    return "left";
  });
}

export function splitTableRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

/** startsTable reports whether a table begins at lines[i] (header + separator). */
export function startsTable(lines: string[], i: number): boolean {
  return isTableRow(lines[i] ?? "") && i + 1 < lines.length && isTableSeparator(lines[i + 1] ?? "");
}

/**
 * parseTableBlock reads the table starting at lines[i]. Call only when
 * startsTable says there is one.
 */
export function parseTableBlock(lines: string[], i: number): TableBlock {
  const start = i;
  const headers = splitTableRow(lines[i] ?? "");
  const aligns = parseTableAligns(lines[i + 1] ?? "");
  let next = i + 2;
  const rows: string[][] = [];
  while (next < lines.length && isTableRow(lines[next] ?? "")) {
    rows.push(splitTableRow(lines[next] ?? ""));
    next++;
  }
  return { headers, aligns, rows, source: lines.slice(start, next).join("\n"), next };
}

/** The inline tokens the chat renderer understands: code, bold, italic, links. */
const inlineTokens = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
const bareURL = /(https?:\/\/[^\s<>)"']+)/g;

/**
 * plainCell strips a cell down to the text it shows: code fences and emphasis
 * markers go, leaving the words. A link keeps both halves as `label (url)` —
 * selecting the rendered table with the mouse would drop the URL, and a
 * plain-text paste is the one place it has nowhere else to survive.
 */
export function plainCell(text: string): string {
  return text.replace(inlineTokens, (token) => {
    if (token.startsWith("`")) return token.slice(1, -1);
    if (token.startsWith("**")) return token.slice(2, -2);
    if (token.startsWith("*")) return token.slice(1, -1);
    const link = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
    if (!link) return token;
    const [, label = "", url = ""] = link;
    return label === url ? label : `${label} (${url})`;
  });
}

export function escapeHTML(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

/**
 * inlineToHTML renders one cell's markdown as HTML, covering the same inline
 * tokens the chat renders, so a pasted table reads like the one on screen
 * rather than showing raw `[text](url)` brackets.
 */
export function inlineToHTML(text: string): string {
  let out = "";
  let last = 0;
  inlineTokens.lastIndex = 0;
  for (;;) {
    const match = inlineTokens.exec(text);
    if (!match) break;
    out += linkifyHTML(text.slice(last, match.index));
    const token = match[0];
    if (token.startsWith("`")) {
      out += `<code>${escapeHTML(token.slice(1, -1))}</code>`;
    } else if (token.startsWith("**")) {
      out += `<strong>${linkifyHTML(token.slice(2, -2))}</strong>`;
    } else if (token.startsWith("*")) {
      out += `<em>${linkifyHTML(token.slice(1, -1))}</em>`;
    } else {
      const link = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      out += link ? `<a href="${escapeHTML(link[2] ?? "")}">${escapeHTML(link[1] ?? "")}</a>` : linkifyHTML(token);
    }
    last = match.index + token.length;
  }
  return out + linkifyHTML(text.slice(last));
}

/** linkifyHTML escapes plain text and turns bare URLs into anchors. */
function linkifyHTML(text: string): string {
  return escapeHTML(text).replace(bareURL, (url) => `<a href="${url}">${url}</a>`);
}

/**
 * tableToHTML renders a parsed table as an HTML table — the flavor Slack, a
 * spreadsheet or a doc reads to rebuild a real table on paste. Alignment rides
 * along as an align attribute.
 */
export function tableToHTML(table: Pick<TableBlock, "headers" | "aligns" | "rows">): string {
  const cell = (tag: "th" | "td", text: string, align: TableAlign | undefined) => {
    const attr = align && align !== "left" ? ` align="${align}"` : "";
    return `<${tag}${attr}>${inlineToHTML(text)}</${tag}>`;
  };
  const head = `<tr>${table.headers.map((h, i) => cell("th", h, table.aligns[i])).join("")}</tr>`;
  const body = table.rows.map((row) => `<tr>${row.map((c, i) => cell("td", c, table.aligns[i])).join("")}</tr>`);
  const parts = ["<table>", `<thead>${head}</thead>`];
  if (body.length > 0) parts.push(`<tbody>${body.join("")}</tbody>`);
  parts.push("</table>");
  return parts.join("\n");
}

/**
 * tableToTSV renders a table as tab-separated rows — the plain-text half of
 * the copy, and on its own enough for a spreadsheet to split into columns.
 */
export function tableToTSV(table: Pick<TableBlock, "headers" | "rows">): string {
  const line = (row: string[]) => row.map(plainCell).join("\t");
  return [line(table.headers), ...table.rows.map(line)].join("\n");
}
