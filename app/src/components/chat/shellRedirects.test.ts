import { describe, expect, it } from "vitest";
import { redirects, segments } from "./shellRedirects";

describe("redirects", () => {
  it.each<[string, string, string[], string?]>([
    ["no redirect", "for i in $(seq 1 60); do echo tick $i; sleep 1; done", []],
    ["stdout and stderr to one file", "make test > /tmp/t.log 2>&1; echo rc=$?", ["/tmp/t.log"]],
    ["each to its own file", "make test > build/test.log 2>build/test.err; echo rc=$?", ["build/test.log", "build/test.err"]],
    ["after a cd", "cd /src/app; s=$(date +%s); make test > build/test.log 2>build/test.err; echo rc=$?", ["/src/app/build/test.log", "/src/app/build/test.err"]],
    ["after a cd chained with &&", "cd /src && make build >build.log 2>&1", ["/src/build.log"]],
    ["after a relative cd", "cd sub && make >../b.log", ["b.log"]],
    ["after cds that go up", "cd /a/b && cd ../c && make > ./x/../out.log", ["/a/c/out.log"]],
    ["but no higher than /", "cd / && make > ../../b.log", ["/b.log"]],
    ["relative and above where it started", "make > ../b.log > . 2> ./", ["../b.log", "."]],
    ["from ~", "make &> ~/logs/b.log", ["~/logs/b.log"]],
    ["~ itself", "cd ~ && make > b.log", ["~/b.log"]],
    ["~ then up out of it", "cd ~/a && make > ../../b.log", ["~/../b.log"]],
    ["appending, both outputs", "make &>> /tmp/b.log", ["/tmp/b.log"]],
    ["with >|", "make >| /tmp/b.log", ["/tmp/b.log"]],
    ["with >& and a file", "make >& /tmp/b.log", ["/tmp/b.log"]],
    ["once though named twice", "a > /tmp/x.log; b >> /tmp/x.log", ["/tmp/x.log"]],
    ["quoted", `make > "/tmp/a b.log" 2> '/tmp/c d.log'`, ["/tmp/a b.log", "/tmp/c d.log"]],
    ["escaped", "make > /tmp/a\\ b.log", ["/tmp/a b.log"]],
    ["but not one with backticks inside double quotes", 'make > "/tmp/`x`.log"', []],
    ["after a command substitution holding a quoted paren", "echo $(echo ')') > /tmp/o.log", ["/tmp/o.log"]],
    ["but not after an unclosed quote in a command substitution", "echo $(echo 'x) > /tmp/o.log", []],
    ["escaped inside double quotes", 'make > "/tmp/a\\"b.log"', ['/tmp/a"b.log']],
    ["but not to /dev/null", "npm run check >/dev/null 2>&1 && echo ok", []],
    ["but not to a descriptor", "make 2>&1 >&2 1>&- | cat", []],
    ["but not to another descriptor than out and err", "exec 3> /tmp/fd3.log", []],
    ["but not one named by a variable", 'make > "$LOG" 2> $' + "{ERR} > /tmp/$(date).log > `x`", []],
    ["but not a glob", "make > /tmp/*.log", []],
    ["but not ~user", "make > ~bob/b.log", []],
    ["but not after a cd to a place not known", "cd $DIR && make > b.log > /abs/c.log", ["/abs/c.log"]],
    ["but not after a bare cd", "cd; make > b.log", []],
    ["but not after cd -", "cd - && make > b.log", []],
    ["but not inside a command substitution", 'O=$(./x.sh 2>/tmp/x.err); echo "$O" > /tmp/o.log', ["/tmp/o.log"]],
    ["but not inside a quoted command substitution", 'echo "$(./x.sh 2>/tmp/x.err)" > /tmp/o.log', ["/tmp/o.log"]],
    ["but not inside backticks", "echo `./x.sh > /tmp/x.err`", []],
    ["but not inside quotes", "nohup bash -c 'make > /tmp/q.log' > /tmp/n.log 2>&1", ["/tmp/n.log"]],
    ["but not into a process substitution", "make > >(tee /tmp/t.log) 2> >(cat)", []],
    ["but not an input", "sort < /tmp/in.txt <<< word > /tmp/s.log", ["/tmp/s.log"]],
    ["but not from a here-document", "cat <<'EOF' > /tmp/h.txt\nmake > /tmp/no.log\nEOF\necho done > /tmp/d.log", ["/tmp/h.txt", "/tmp/d.log"]],
    ["but not from a tab-indented here-document", "cat <<-EOF > /tmp/h.txt\n\tmake > /tmp/no.log\n\tEOF\necho x > /tmp/d.log", ["/tmp/h.txt", "/tmp/d.log"]],
    ["but not in a comment", "make > /tmp/b.log # > /tmp/no.log\n# echo > /tmp/no2.log", ["/tmp/b.log"]],
    ["in a subshell", "(cd /s && make > b.log) & wait", ["/s/b.log"]],
    ["after a backslash-newline", "make \\\n  > /tmp/b.log", ["/tmp/b.log"]],
    ["held back by tail", "go test ./... 2>&1 | tail -20", [], "tail"],
    ["held back by grep after a redirect", "make > /tmp/b.log; grep -c ok /tmp/b.log | head -1; echo done", ["/tmp/b.log"], "head"],
    ["not held back by cat", "make | cat", []],
    ["not held back by an or", "make || tail -1 x", []],
    ["held back after |&", "make |& grep err", [], "grep"],
  ])("finds those %s", (_name, command, files, heldBy) => {
    expect(redirects(command)).toEqual(heldBy ? { files, heldBy } : { files });
  });

  it("finds the log of a run with env vars and a command substitution", () => {
    const command = "LEVEL=debug KEY=$(cat secret.txt) ./bin/app --config app.yaml --report out/report.json > out/run.log 2>&1; echo rc=$?";
    expect(redirects(command).files).toEqual(["out/run.log"]);
  });

  it("reads an unclosed quote or substitution to the end", () => {
    expect(redirects("make > /tmp/b.log; echo 'oops")).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects('make > /tmp/b.log; echo "oops')).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects("make > /tmp/b.log; echo $(oops")).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects("make > /tmp/b.log; echo $(echo 'a)' \\) ok")).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects("make > /tmp/b.log; echo `oops")).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects("make > /tmp/b.log; echo ${oops")).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects("cat <<EOF > /tmp/h.txt\nno end")).toEqual({ files: ["/tmp/h.txt"] });
    expect(redirects("make > /tmp/b.log \\")).toEqual({ files: ["/tmp/b.log"] });
    expect(redirects("make >")).toEqual({ files: [] });
    expect(redirects("make > ''")).toEqual({ files: [] });
  });
});

describe("segments", () => {
  it("splits on every operator", () => {
    const names = segments("a; b && c || d | e |& f & g\nh (i) {").map((s) => s.words[0]?.text);
    expect(names).toEqual(["a", "b", "c", "d", "e", "f", "g", "h", "i", "{"]);
  });

  it("keeps a digit word that isn't a descriptor", () => {
    const [seg] = segments("echo 2 > /tmp/x");
    expect(seg?.words.map((w) => w.text)).toEqual(["echo", "2"]);
    expect(seg?.redirects.map((r) => r.fd)).toEqual(["1"]);
  });
});
