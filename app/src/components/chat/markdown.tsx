import { useContext } from "react";
import { FileLink } from "./FileLink";
import { findCandidatePaths } from "../../utils/fileLinks";
import { ChannelContext, buildMessageStyles } from "./chatShared";
import { useTheme } from "../../ThemeContext";

function isTableRow(line: string): boolean {
  return line.includes("|") && line.trim().length > 0 && !line.trim().startsWith("```");
}

function isTableSeparator(line: string): boolean {
  // |---|:---:|---:| with optional surrounding pipes/whitespace.
  return /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(line);
}

function parseTableAligns(separator: string): ("left" | "center" | "right")[] {
  return splitTableRow(separator).map((cell) => {
    const t = cell.trim();
    const left = t.startsWith(":");
    const right = t.endsWith(":");
    if (left && right) return "center";
    if (right) return "right";
    return "left";
  });
}

function splitTableRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

function linkifyText(text: string, keyBase: number, channelId: string): React.ReactNode[] {
  // Collect URL and file-path matches, then merge by start position. File-path
  // matches that overlap a URL match are dropped (URLs win — they often contain
  // a `.ext` suffix that would otherwise be mis-detected as a path).
  const urlRegex = /(https?:\/\/[^\s<>)"']+)/g;
  type Hit =
    | { kind: "url"; start: number; length: number; href: string }
    | { kind: "path"; start: number; length: number; raw: string; line: number | null };
  const hits: Hit[] = [];
  for (;;) {
    const m = urlRegex.exec(text);
    if (!m) break;
    hits.push({ kind: "url", start: m.index, length: m[0].length, href: m[0] });
  }
  if (channelId) {
    for (const c of findCandidatePaths(text)) {
      if (hits.some((h) => h.kind === "url" && c.start >= h.start && c.start < h.start + h.length)) continue;
      hits.push({ kind: "path", start: c.start, length: c.length, raw: c.raw, line: c.line });
    }
  }
  hits.sort((a, b) => a.start - b.start);

  const parts: React.ReactNode[] = [];
  let last = 0;
  for (const h of hits) {
    if (h.start < last) continue; // overlapping (shouldn't happen after URL filter, but be safe)
    if (h.start > last) parts.push(text.slice(last, h.start));
    if (h.kind === "url") {
      parts.push(
        <a
          key={`link-${keyBase}-${parts.length}`}
          href={h.href}
          target="_blank"
          rel="noopener noreferrer"
          style={{ color: "#6ba3f7", textDecoration: "underline" }}
        >
          {h.href}
        </a>,
      );
    } else {
      parts.push(
        <FileLink
          key={`file-${keyBase}-${parts.length}`}
          channelId={channelId}
          raw={h.raw}
          line={h.line}
        />,
      );
    }
    last = h.start + h.length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

function formatInline(text: string, s: Record<string, React.CSSProperties>, channelId: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Match inline code, bold, italic, markdown links.
  const regex = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
  let lastIndex = 0;

  for (;;) {
    const match = regex.exec(text);
    if (!match) break;

    if (match.index > lastIndex) {
      nodes.push(...linkifyText(text.slice(lastIndex, match.index), nodes.length, channelId));
    }

    const token = match[0];
    if (token.startsWith("`")) {
      nodes.push(
        <code key={nodes.length} style={s.inlineCode}>
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("**")) {
      nodes.push(
        <strong key={nodes.length}>{token.slice(2, -2)}</strong>,
      );
    } else if (token.startsWith("*")) {
      nodes.push(<em key={nodes.length}>{token.slice(1, -1)}</em>);
    } else if (token.startsWith("[")) {
      const mdMatch = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      if (mdMatch) {
        nodes.push(
          <a
            key={nodes.length}
            href={mdMatch[2]}
            target="_blank"
            rel="noopener noreferrer"
            style={{ color: "#6ba3f7", textDecoration: "underline" }}
          >
            {mdMatch[1]}
          </a>,
        );
      }
    }

    lastIndex = match.index + token.length;
  }

  if (lastIndex < text.length) {
    nodes.push(...linkifyText(text.slice(lastIndex), nodes.length, channelId));
  }

  return nodes;
}

function parseMarkdown(text: string, s: Record<string, React.CSSProperties>, channelId: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  const lines = text.split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i] ?? "";

    // Fenced code block.
    if (line.startsWith("```")) {
      const lang = line.slice(3).trim();
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !(lines[i] ?? "").startsWith("```")) {
        codeLines.push(lines[i] ?? "");
        i++;
      }
      i++; // skip closing ```
      nodes.push(
        <pre key={nodes.length} style={s.codeBlock}>
          {lang && <div style={s.codeLang}>{lang}</div>}
          <code>{codeLines.join("\n")}</code>
        </pre>,
      );
      continue;
    }

    // GFM table: header row + separator (|---|---|) + body rows.
    if (isTableRow(line) && i + 1 < lines.length && isTableSeparator(lines[i + 1] ?? "")) {
      const aligns = parseTableAligns(lines[i + 1] ?? "");
      const headers = splitTableRow(line);
      i += 2;
      const bodyRows: string[][] = [];
      while (i < lines.length && isTableRow(lines[i] ?? "")) {
        bodyRows.push(splitTableRow(lines[i] ?? ""));
        i++;
      }
      nodes.push(
        <table key={nodes.length} style={s.table}>
          <thead>
            <tr>
              {headers.map((h, hi) => (
                <th key={hi} style={{ ...s.tableHeaderCell, textAlign: aligns[hi] ?? "left" }}>
                  {formatInline(h, s, channelId)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {bodyRows.map((row, ri) => (
              <tr key={ri}>
                {row.map((cell, ci) => (
                  <td key={ci} style={{ ...s.tableCell, textAlign: aligns[ci] ?? "left" }}>
                    {formatInline(cell, s, channelId)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>,
      );
      continue;
    }

    // Blockquote: collect consecutive `> ` lines.
    if (line.startsWith("> ") || line === ">") {
      const quoteLines: string[] = [];
      while (i < lines.length && ((lines[i] ?? "").startsWith("> ") || (lines[i] ?? "") === ">")) {
        const ql = lines[i] ?? "";
        quoteLines.push(ql === ">" ? "" : ql.slice(2));
        i++;
      }
      nodes.push(
        <blockquote key={nodes.length} style={s.blockquote}>
          {quoteLines.map((ql, qi) => (
            <p key={qi} style={s.paragraph}>{ql ? formatInline(ql, s, channelId) : <br />}</p>
          ))}
        </blockquote>,
      );
      continue;
    }

    // Regular line — apply inline formatting.
    if (line.trim() === "") {
      nodes.push(<br key={nodes.length} />);
    } else {
      nodes.push(
        <p key={nodes.length} style={s.paragraph}>
          {formatInline(line, s, channelId)}
        </p>,
      );
    }
    i++;
  }

  return nodes;
}

export function MarkdownContent({ content }: { content: string }) {
  const { colors } = useTheme();
  const channelId = useContext(ChannelContext);
  const s = buildMessageStyles(colors);
  const parts = parseMarkdown(content, s, channelId);
  return <>{parts}</>;
}
