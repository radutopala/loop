import "katex/dist/katex.min.css";
import "./math.css";
import { useContext, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { findCandidatePaths } from "../../utils/fileLinks";
import { inAppHref } from "../../utils/messageLinks";
import { CopyButton } from "../shared/CopyButton";
import { ChatComponent } from "./ChatComponent";
import { buildMessageStyles, ChannelContext } from "./chatShared";
import { parseComponentInfo, readFence } from "./componentFence";
import { FileLink } from "./FileLink";
import { parseTableBlock, startsTable, type TableAlign, tableToHTML, tableToTSV } from "./markdownTable";
import { findMathBlock, inlineMathPattern, inlineMathTeX, isInlineMath, renderMath } from "./math";

// Link opens a web link in a new window, and a loop://channel/ link to a
// channel or a message in the app itself.
function Link({ href, children }: { href: string; children: React.ReactNode }) {
  const style = { color: "#6ba3f7", textDecoration: "underline" };
  const inApp = inAppHref(href);
  if (inApp) {
    return (
      <a href={inApp} style={style}>
        {children}
      </a>
    );
  }
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" style={style}>
      {children}
    </a>
  );
}

function linkifyText(text: string, keyBase: number, channelId: string): React.ReactNode[] {
  // Collect URL and file-path matches, then merge by start position. File-path
  // matches that overlap a URL match are dropped (URLs win — they often contain
  // a `.ext` suffix that would otherwise be mis-detected as a path).
  const urlRegex = /((?:https?:\/\/|loop:\/\/channel\/)[^\s<>)"']+)/g;
  type Hit = { kind: "url"; start: number; length: number; href: string } | { kind: "path"; start: number; length: number; raw: string; line: number | null };
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
        <Link key={`link-${keyBase}-${parts.length}`} href={h.href}>
          {h.href}
        </Link>,
      );
    } else {
      parts.push(<FileLink key={`file-${keyBase}-${parts.length}`} channelId={channelId} raw={h.raw} line={h.line} />);
    }
    last = h.start + h.length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

function formatInline(text: string, s: Record<string, React.CSSProperties>, channelId: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Match inline code, math, bold, italic, markdown links. Math comes before
  // bold and italic so the * in a formula isn't read as emphasis.
  const regex = new RegExp(`(\`[^\`]+\`|${inlineMathPattern}|\\*\\*[^*]+\\*\\*|\\*[^*]+\\*|\\[[^\\]]+\\]\\([^)]+\\))`, "g");
  let lastIndex = 0;

  for (;;) {
    const match = regex.exec(text);
    if (!match) break;

    if (match.index > lastIndex) {
      nodes.push(...linkifyText(text.slice(lastIndex, match.index), nodes.length, channelId));
    }

    const token = match[0];
    if (isInlineMath(token)) {
      nodes.push(<span key={nodes.length} dangerouslySetInnerHTML={{ __html: renderMath(inlineMathTeX(token), false) }} />);
    } else if (token.startsWith("`")) {
      nodes.push(
        <code key={nodes.length} style={s.inlineCode}>
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("**")) {
      // Format inside bold/italic too, so a bare URL (**https://…**) stays
      // clickable and a formula (**$x = 1$**) renders. Their content has no *,
      // so this can't recurse forever.
      nodes.push(<strong key={nodes.length}>{formatInline(token.slice(2, -2), s, channelId)}</strong>);
    } else if (token.startsWith("*")) {
      nodes.push(<em key={nodes.length}>{formatInline(token.slice(1, -1), s, channelId)}</em>);
    } else if (token.startsWith("[")) {
      const mdMatch = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
      if (mdMatch) {
        nodes.push(
          <Link key={nodes.length} href={mdMatch[2]!}>
            {mdMatch[1]}
          </Link>,
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

/** The grid glyph marking the copy-as-table button. */
const tableIcon = (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <rect x="3" y="4" width="18" height="16" rx="2" />
    <line x1="3" y1="10" x2="21" y2="10" />
    <line x1="9" y1="10" x2="9" y2="20" />
  </svg>
);

/** The `</>` glyph marking the copy-as-HTML-markup button. */
const markupIcon = (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="16 18 22 12 16 6" />
    <polyline points="8 6 2 12 8 18" />
  </svg>
);

/**
 * A GFM table with three hover-revealed copy buttons in a gutter on its
 * right, one per flavor a table usually has to travel in:
 *
 *   - markdown, the table's own source, for anywhere that speaks markdown;
 *   - a real table: HTML as rich text with tab-separated text alongside it,
 *     the pair a browser writes for a selected table. Chat and office apps
 *     read the HTML half and rebuild the table; only Slack makes a table out
 *     of the tabs alone, so the HTML half is what Teams, Sheets and docs need;
 *   - the HTML markup itself, as plain text, for pasting into a page or a
 *     template.
 *
 * Links survive all three: as markdown, as an anchor, and as `label (url)`.
 */
function MarkdownTable({
  source,
  aligns,
  headers,
  rows,
  s,
  channelId,
}: {
  source: string;
  aligns: TableAlign[];
  headers: string[];
  rows: string[][];
  s: Record<string, React.CSSProperties>;
  channelId: string;
}) {
  const [hovered, setHovered] = useState(false);
  // Built on render rather than on click: the tables here are chat-sized, and
  // it keeps the button a pure copy.
  const html = tableToHTML({ headers, aligns, rows });
  const tsv = tableToTSV({ headers, rows });
  return (
    // inline-block so the wrapper hugs the table and the buttons track the
    // table's right edge, not the full width of the message column. The right
    // padding is their gutter — parked over the header row instead, they hide
    // the last column's heading whenever that column is narrow. minHeight
    // keeps the lowest button off whatever follows a short table.
    <div style={{ position: "relative", display: "inline-block", maxWidth: "100%", paddingRight: 24, minHeight: 82 }} onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}>
      <CopyButton text={source} visible={hovered} title="Copy as markdown" style={{ position: "absolute", top: 13, right: 0 }} />
      <CopyButton text={tsv} html={html} icon={tableIcon} visible={hovered} title="Copy as table (Slack, Teams, Sheets, docs)" style={{ position: "absolute", top: 36, right: 0 }} />
      <CopyButton text={html} icon={markupIcon} visible={hovered} title="Copy as HTML markup" style={{ position: "absolute", top: 59, right: 0 }} />
      <table style={s.table}>
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
          {rows.map((row, ri) => (
            <tr key={ri}>
              {row.map((cell, ci) => (
                <td key={ci} style={{ ...s.tableCell, textAlign: aligns[ci] ?? "left" }}>
                  {formatInline(cell, s, channelId)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** A display formula, centered, with a hover-revealed button that copies its LaTeX. */
function MathDisplay({ tex, source }: { tex: string; source: string }) {
  const [hovered, setHovered] = useState(false);
  return (
    <div
      data-testid="math-display"
      className="loop-chat-math"
      style={{ position: "relative", margin: "4px 0", padding: "0 24px" }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <CopyButton text={source} visible={hovered} title="Copy as LaTeX" style={{ position: "absolute", top: 0, right: 0 }} />
      {/* KaTeX output, without trust, is markup only: no scripts, links or handlers. */}
      <div style={{ overflowX: "auto", overflowY: "hidden" }} dangerouslySetInnerHTML={{ __html: renderMath(tex, true) }} />
    </div>
  );
}

function parseMarkdown(text: string, s: Record<string, React.CSSProperties>, channelId: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  const lines = text.split("\n");
  let i = 0;

  while (i < lines.length) {
    const line = lines[i] ?? "";

    // Fenced block: a chat component once it's closed, else code.
    const fence = readFence(lines, i);
    if (fence) {
      i = fence.next;
      const component = fence.closed ? parseComponentInfo(fence.info) : null;
      if (component) {
        nodes.push(<ChatComponent key={nodes.length} template={component.template} title={component.title} doc={fence.body} />);
        continue;
      }
      nodes.push(
        <pre key={nodes.length} style={s.codeBlock}>
          {fence.info && <div style={s.codeLang}>{fence.info}</div>}
          <code>{fence.body}</code>
        </pre>,
      );
      continue;
    }

    // Display math: $$…$$ or \[…\], on one line or several.
    const math = findMathBlock(lines, i);
    if (math) {
      i = math.next;
      nodes.push(<MathDisplay key={nodes.length} tex={math.tex} source={math.source} />);
      continue;
    }

    // GFM table: header row + separator (|---|---|) + body rows.
    if (startsTable(lines, i)) {
      const table = parseTableBlock(lines, i);
      i = table.next;
      nodes.push(<MarkdownTable key={nodes.length} source={table.source} aligns={table.aligns} headers={table.headers} rows={table.rows} s={s} channelId={channelId} />);
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
            <p key={qi} style={s.paragraph}>
              {ql ? formatInline(ql, s, channelId) : <br />}
            </p>
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
