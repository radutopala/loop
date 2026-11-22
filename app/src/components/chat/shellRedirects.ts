/**
 * Reads, from a shell command, the files its output is redirected to, so
 * that a background command which writes its log to a file (`make test >
 * build.log 2>&1; echo rc=$?`) can be followed there: the CLI's own file for
 * the task only gets what isn't redirected.
 *
 * It reads the command as the shell splits it (quotes, escapes, `$(…)`,
 * backticks, comments, here-documents), but runs nothing: a word the shell
 * would change, by a variable, a command substitution or a glob, is left
 * out, as the file it names isn't known.
 */

/** A word of the command with its quotes removed. */
interface Word {
  text: string;
  /** Whether the shell would change it: an expansion or a glob. */
  dynamic: boolean;
  /** Whether it starts with an unquoted ~. */
  tilde: boolean;
}

interface Redirect {
  /** The file descriptor redirected: a number, or & for both outputs, < for
   *  an input. */
  fd: string;
  /** Whether the operator was >&, whose target is a descriptor when it's a
   *  number. */
  dup: boolean;
  target: Word;
}

/** A simple command: its words and redirects, and whether a pipe feeds it. */
interface Segment {
  words: Word[];
  redirects: Redirect[];
  piped: boolean;
}

/** The index after the parenthesis that closes the one at open. */
function closeParen(cmd: string, open: number): number {
  let depth = 0;
  for (let i = open; i < cmd.length; i++) {
    const c = cmd[i];
    if (c === "\\") i++;
    else if (c === "'") i = cmd.indexOf("'", i + 1) < 0 ? cmd.length : cmd.indexOf("'", i + 1);
    else if (c === "(") depth++;
    else if (c === ")" && --depth === 0) return i + 1;
  }
  return cmd.length;
}

/** The index after the next ch from i. */
function after(cmd: string, i: number, ch: string): number {
  const j = cmd.indexOf(ch, i);
  return j < 0 ? cmd.length : j + 1;
}

/** The index after the here-documents' bodies, which start at i, one per
 *  delimiter in turn. */
function skipHeredocs(cmd: string, i: number, delimiters: string[]): number {
  for (const d of delimiters) {
    while (i < cmd.length) {
      const end = after(cmd, i, "\n");
      const line = cmd.slice(i, end).replace(/\n$/, "");
      i = end;
      if (line.replace(/^\t+/, "") === d) break;
    }
  }
  return i;
}

/** Splits cmd into its simple commands. */
export function segments(cmd: string): Segment[] {
  const out: Segment[] = [];
  let seg: Segment = { words: [], redirects: [], piped: false };
  let word: Word | null = null;
  let redirect: { fd: string; dup: boolean } | null = null;
  let heredoc = false;
  let heredocs: string[] = [];
  const w = () => (word ??= { text: "", dynamic: false, tilde: false });
  const endWord = () => {
    if (!word) return;
    if (redirect) seg.redirects.push({ ...redirect, target: word });
    else if (heredoc) heredocs.push(word.text);
    else seg.words.push(word);
    redirect = null;
    heredoc = false;
    word = null;
  };
  const endSegment = (piped = false) => {
    endWord();
    redirect = null;
    heredoc = false;
    if (seg.words.length || seg.redirects.length) out.push(seg);
    seg = { words: [], redirects: [], piped };
  };

  let i = 0;
  while (i < cmd.length) {
    const c = cmd[i];
    const next = cmd[i + 1];
    if (c === "\\") {
      if (next !== "\n") w().text += next ?? "";
      i += 2;
    } else if (c === "'") {
      const end = after(cmd, i + 1, "'");
      w().text += cmd.slice(i + 1, end - 1);
      i = end;
    } else if (c === '"') {
      const wd = w();
      for (i++; i < cmd.length && cmd[i] !== '"'; ) {
        if (cmd[i] === "\\" && i + 1 < cmd.length) {
          wd.text += cmd[i + 1];
          i += 2;
        } else if (cmd[i] === "$" || cmd[i] === "`") {
          wd.dynamic = true;
          if (cmd[i] === "`") i = after(cmd, i + 1, "`");
          else if (cmd[i + 1] === "(") i = closeParen(cmd, i + 1);
          else i++;
        } else wd.text += cmd[i++];
      }
      i++;
    } else if (c === "$") {
      w().dynamic = true;
      if (next === "(") i = closeParen(cmd, i + 1);
      else if (next === "{") i = after(cmd, i + 2, "}");
      else i++;
    } else if (c === "`") {
      w().dynamic = true;
      i = after(cmd, i + 1, "`");
    } else if (c === "#" && !word) {
      const eol = cmd.indexOf("\n", i);
      i = eol < 0 ? cmd.length : eol;
    } else if (c === " " || c === "\t") {
      endWord();
      i++;
    } else if (c === "\n") {
      endSegment();
      i = skipHeredocs(cmd, i + 1, heredocs);
      heredocs = [];
    } else if (c === ";" || c === "(" || c === ")") {
      endSegment();
      i++;
    } else if (c === "|") {
      // || runs the next command on failure; | and |& pipe into it.
      endSegment(next !== "|");
      i += next === "|" || next === "&" ? 2 : 1;
    } else if (c === "&" && next !== ">") {
      endSegment();
      i += next === "&" ? 2 : 1;
    } else if (c === "<" || c === ">" || c === "&") {
      let fd = c === "&" ? "&" : c === "<" ? "<" : "1";
      const prefix = word as Word | null;
      if (c === ">" && prefix && !prefix.dynamic && /^\d+$/.test(prefix.text)) {
        fd = prefix.text;
        word = null;
      } else endWord();
      if (c === "&") i++;
      if (c === "<" && next === "<") {
        if (cmd[i + 2] === "<") {
          // A here-string: its word is input.
          i += 3;
          redirect = { fd: "<", dup: false };
        } else {
          i += cmd[i + 2] === "-" ? 3 : 2;
          heredoc = true;
        }
        continue;
      }
      i++;
      if (cmd[i] === ">" || cmd[i] === "|") i++;
      if (cmd[i] === "(") {
        // A process substitution, >(…) or <(…), is no file.
        i = closeParen(cmd, i);
        continue;
      }
      const dup = cmd[i] === "&";
      if (dup) i++;
      redirect = { fd, dup };
    } else {
      if (c === "~" && !word) w().tilde = true;
      if (c === "*" || c === "?" || c === "[") w().dynamic = true;
      w().text += c;
      i++;
    }
  }
  endSegment();
  return out;
}

/** Resolves the parts of path, keeping a leading / or ~. */
function normalize(path: string): string {
  const root = path.startsWith("/") ? "/" : path === "~" || path.startsWith("~/") ? "~/" : "";
  const parts: string[] = [];
  for (const p of path.slice(root.length).split("/")) {
    if (p === "" || p === ".") continue;
    if (p === ".." && parts.length && parts[parts.length - 1] !== "..") parts.pop();
    else if (p !== ".." || root !== "/") parts.push(p);
  }
  const joined = parts.join("/");
  if (root === "~/") return joined ? `~/${joined}` : "~";
  return root + joined || ".";
}

/** Where a word points from dir: undefined when the shell's directory
 *  hasn't changed, null when it isn't known. Null for a word the shell
 *  would change, ~user among them. */
function resolve(dir: string | null | undefined, word: Word): string | null {
  if (word.dynamic || !word.text) return null;
  if (word.tilde) return word.text === "~" || word.text.startsWith("~/") ? normalize(word.text) : null;
  if (word.text.startsWith("/")) return normalize(word.text);
  if (dir === null) return null;
  return normalize(dir === undefined ? word.text : `${dir}/${word.text}`);
}

/** Filters that hold their input back until it ends, or in blocks. */
const HOLDING_FILTERS = new Set(["tail", "head", "grep", "egrep", "rg", "jq", "sort", "uniq", "wc", "awk", "sed", "cut", "tr", "column", "python", "python3"]);

export interface Redirects {
  /** The files the command writes its output to, in order: absolute, from
   *  ~, or relative to the directory it started in. */
  files: string[];
  /** The filter the command's output last goes through, when it's one that
   *  holds the output back, e.g. tail. */
  heldBy?: string;
}

/** Reads command's output redirects; see the module comment. A cd before
 *  them moves the files it names, until one to a place not known. */
export function redirects(command: string): Redirects {
  const files: string[] = [];
  let heldBy: string | undefined;
  let dir: string | null | undefined;
  for (const seg of segments(command)) {
    const [name, arg] = seg.words;
    if (name && !name.dynamic && name.text === "cd") dir = arg && arg.text !== "-" ? resolve(dir, arg) : null;
    for (const r of seg.redirects) {
      if (r.fd !== "1" && r.fd !== "2" && r.fd !== "&") continue;
      if (r.dup && /^(\d+|-)$/.test(r.target.text)) continue;
      const file = resolve(dir, r.target);
      if (file && !file.startsWith("/dev/") && !files.includes(file)) files.push(file);
    }
    if (seg.piped && name && HOLDING_FILTERS.has(name.text)) heldBy = name.text;
  }
  return heldBy ? { files, heldBy } : { files };
}
