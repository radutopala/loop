import { describe, expect, it } from "vitest";
import { codeToFence, codeToHTML } from "./codeBlock";

describe("codeToFence", () => {
  const cases: { name: string; body: string; info: string; want: string }[] = [
    { name: "plain text", body: "first line\nsecond line", info: "", want: "```\nfirst line\nsecond line\n```" },
    { name: "keeps the language", body: "go test ./...", info: "sh", want: "```sh\ngo test ./...\n```" },
    { name: "outlasts a fence inside", body: "```js\nx()\n```", info: "md", want: "````md\n```js\nx()\n```\n````" },
    { name: "inline code stays under three", body: "use `x`", info: "", want: "```\nuse `x`\n```" },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(codeToFence(c.body, c.info)).toBe(c.want);
    });
  }
});

describe("codeToHTML", () => {
  const cases: { name: string; body: string; info: string; want: string }[] = [
    { name: "no language", body: "a\nb", info: "", want: "<pre><code>a\nb</code></pre>" },
    { name: "language from the first word", body: "x", info: "ts title=a.ts", want: '<pre><code class="language-ts">x</code></pre>' },
    { name: "escapes markup", body: '<a href="x">&</a>', info: "", want: "<pre><code>&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;</code></pre>" },
    { name: "escapes the language", body: "x", info: '"><b>', want: '<pre><code class="language-&quot;&gt;&lt;b&gt;">x</code></pre>' },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(codeToHTML(c.body, c.info)).toBe(c.want);
    });
  }
});
