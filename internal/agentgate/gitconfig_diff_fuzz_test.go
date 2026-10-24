package agentgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// FuzzParseGitConfigStrictMatchesGit is a differential spec: whenever the
// strict parser accepts a config, git must read exactly the same entries.
// A mismatch means the git guard could approve a change git interprets
// differently. Skips when git is not installed.
func FuzzParseGitConfigStrictMatchesGit(f *testing.F) {
	git := specGit(f)
	for _, seed := range []string{
		"[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n",
		"[remote \"origin\"]\n\turl = https://github.com/a/b\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n",
		"[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n",
		"[user]\nname = A B\nemail = a@b.c\n",
		"[core]\nbare\n",
		"# c\n; c\n[Core]\nFileMode=TRUE\n",
	} {
		f.Add([]byte(seed))
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, b []byte) {
		ours, ok := parseGitConfigStrict(b)
		if !ok {
			return
		}
		p := filepath.Join(dir, "config")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(git, "config", "--file", p, "--list", "--null")
		// Outside any repo, so git's discovery from the package dir can't fail.
		cmd.Dir, cmd.Env = dir, specGitEnv(dir)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("strict parser accepted %q but git rejects it: %v", b, err)
		}
		var theirs []string
		for _, rec := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
			if rec == "" {
				continue
			}
			k, v, found := strings.Cut(rec, "\n")
			if !found {
				v = "" // valueless boolean; the strict parser also stores ""
			}
			theirs = append(theirs, k+"="+v)
		}
		var mine []string
		for _, e := range ours {
			k := e.section
			if e.sub != "" {
				k += "." + e.sub
			}
			k += "." + e.key
			mine = append(mine, k+"="+e.value)
		}
		if !slices.Equal(mine, theirs) {
			t.Fatalf("parser differential on %q\n ours: %q\n  git: %q", b, mine, theirs)
		}
	})
}
