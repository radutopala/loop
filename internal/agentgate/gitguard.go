package agentgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/radutopala/loop/internal/types"
)

// The git guard protects the files that make git run code: a repo's config
// (core.fsmonitor, core.sshCommand, filter drivers, aliases, …), its hooks,
// and the pointers that send git to another git dir (a .git file, commondir).
// Under a host-backed mount these reach the user's own git on the host.
//
// A write the gate can't see the content of is refused outright: open(2) for
// create/write, truncate, link, symlink, mknod. Git itself never writes these
// files in place — it writes a lock file and renames it over the target — so
// a rename onto a guarded path is where content shows up. The gate reads the
// source, decides, and on allow performs the write itself with exactly the
// bytes it read (see GuardFS): the agent can't swap the content between the
// decision and the rename, and its own inode never lands on a guarded path.
//
// A config change that only adds entries from safeGitConfigKeys (what
// git init, clone, remote add, branch -u and user.* write) goes through
// silently. Anything else — a hook, a config key outside that set, a .git
// pointer — asks with the diff on the card.

// maxGuardedFileSize caps what the guard reads and shows on a card. Real
// configs and hooks are a few KB; a bigger file is refused rather than
// shown truncated.
const maxGuardedFileSize = 32 << 10

// gitGuardRuleID tags the guard's audit entries.
const gitGuardRuleID = "git-guard"

type gitPathKind int

const (
	gitNone      gitPathKind = iota
	gitEntry                 // the .git entry itself: a git dir, or a "gitdir:" file
	gitConfig                // config, config.worktree
	gitPointer               // commondir: points git at another git dir
	gitHook                  // anything under hooks/, except the *.sample files
	gitHooksRoot             // the hooks dir itself
)

// GuardSnapshot is what the guard read for one rename: the source's bytes
// and mode, and the target's current content for the diff. Install writes
// Data — never re-reads the source.
type GuardSnapshot struct {
	Src, Dst  string
	Data      []byte
	Mode      os.FileMode
	Old       []byte
	OldExists bool
	UID, GID  int
}

// GuardFS reads and installs guarded files as the tracee's uid/gid, so the
// gate (root) never grants the agent access it doesn't have.
type GuardFS interface {
	// Snapshot reads src (a regular file of at most maxGuardedFileSize) and
	// dst's current content. Refuses symlinks in either parent path.
	Snapshot(src, dst string, uid, gid int) (*GuardSnapshot, error)
	// Install writes snap.Data to snap.Dst atomically (temp file + rename)
	// and removes snap.Src. noReplace mirrors RENAME_NOREPLACE.
	Install(snap *GuardSnapshot, noReplace bool) error
	// IsRegular reports whether path (not following a final symlink) is a
	// regular file.
	IsRegular(path string) (bool, error)
}

// ErrGuardNotRegular is returned by GuardFS.Snapshot when the rename source
// is not a regular file (a directory, symlink, fifo, …).
var ErrGuardNotRegular = errors.New("agentgate: git guard: source is not a regular file")

// ErrGuardTooLarge is returned by GuardFS.Snapshot when the source exceeds
// maxGuardedFileSize.
var ErrGuardTooLarge = errors.New("agentgate: git guard: source is too large to review")

// GitGuard applies the rules above under Roots — the container paths backed
// by a writable host mount. Scratch repos elsewhere (/tmp) never reach the
// host and aren't guarded. A nil *GitGuard guards nothing.
type GitGuard struct {
	Roots      []string
	FS         GuardFS
	Approver   Approver
	Auditor    Auditor
	PeerSource PeerSourceLookup
	Now        func() time.Time
}

// GuardRename is one rename onto (or, with RENAME_EXCHANGE, from) a guarded
// path. Src/Dst are resolved absolute paths.
type GuardRename struct {
	TrapID    uint64
	PID       int
	ChannelID string
	Syscall   string
	Src, Dst  string
	Flags     uint64
	Tracee    Tracee
}

// Protects reports whether path is a guarded git path.
func (g *GitGuard) Protects(path string) bool {
	return g.classify(path) != gitNone
}

// AllowsInPlace reports whether a single-path op on a guarded path can go
// on to the normal policy. Reads, deletes and permission changes can;
// mkdir of an empty dir can. Creates and writes whose content the gate
// can't see can't.
func AllowsInPlace(spec SyscallSpec, op string) bool {
	switch op {
	case OpCreate:
		return spec.Mkdir
	case OpWrite, OpLink:
		return false
	default:
		return true
	}
}

// Refuse records a refused in-place op on a guarded path and returns the
// EPERM reply.
func (g *GitGuard) Refuse(trapID uint64, pid int, channelID, op, path string) TrapResponse {
	g.audit(pid, channelID, op+" "+path, string(types.DecisionDeny), "", nil)
	return denyResp(trapID, syscall.EPERM)
}

// InsideGitDir reports whether path lies below a .git dir under a root.
func (g *GitGuard) InsideGitDir(path string) bool {
	rel, ok := g.rel(path)
	if !ok {
		return false
	}
	comps := strings.Split(rel, "/")
	for _, c := range comps[:len(comps)-1] {
		if strings.EqualFold(c, ".git") {
			return true
		}
	}
	return false
}

// TreeRename checks a rename into a git dir onto a path that isn't itself
// guarded. Git renames files there all the time (refs, the index, objects),
// and those go on to the file rules: ok=false. A directory or symlink would
// bring a whole tree in unreviewed — say a submodule's git dir under
// .git/modules with its own hooks and config — so it's refused, as is
// RENAME_EXCHANGE, which git never uses.
func (g *GitGuard) TreeRename(r GuardRename) (resp TrapResponse, refused bool) {
	if !g.InsideGitDir(r.Dst) && (r.Flags&renameExchange == 0 || !g.InsideGitDir(r.Src)) {
		return TrapResponse{}, false
	}
	if r.Flags&renameExchange != 0 {
		return g.refuse(r), true
	}
	regular, err := g.FS.IsRegular(r.Src)
	if err != nil {
		return g.fail(r, OpWrite+" "+r.Dst, err), true
	}
	if !regular {
		return g.refuse(r), true
	}
	return TrapResponse{}, false
}

func (g *GitGuard) classify(path string) gitPathKind {
	rel, ok := g.rel(path)
	if !ok {
		return gitNone
	}
	return classifyGitRel(rel)
}

// rel returns path relative to the first root holding it.
func (g *GitGuard) rel(path string) (string, bool) {
	if g == nil {
		return "", false
	}
	path = filepath.Clean(path)
	for _, root := range g.Roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		return rel, true
	}
	return "", false
}

// classifyGitRel classifies a root-relative path. Components compare
// case-insensitively (with Unicode folding): the host filesystem behind a
// mount is often case-insensitive, where .GIT/HOOKS/pre-commit is the same
// file as .git/hooks/pre-commit.
func classifyGitRel(rel string) gitPathKind {
	comps := strings.Split(rel, "/")
	for i, c := range comps {
		if !strings.EqualFold(c, ".git") {
			continue
		}
		if k := classifyGitDir(comps[i+1:]); k != gitNone {
			return k
		}
	}
	return gitNone
}

// classifyGitDir classifies r, the components below a .git dir.
func classifyGitDir(r []string) gitPathKind {
	if len(r) == 0 {
		return gitEntry
	}
	if len(r) == 1 {
		return classifyGitLeaf(r[0])
	}
	if !foldAny(r[0], "hooks", "modules", "worktrees") {
		return gitNone
	}
	// hooks/<name>, or a nested git dir: submodules (modules/<name…>, the
	// name may hold slashes, and they nest) and linked worktrees
	// (worktrees/<name>).
	for j, c := range r {
		if !strings.EqualFold(c, "hooks") {
			continue
		}
		switch {
		case j == len(r)-1:
			return gitHooksRoot
		case j == len(r)-2 && strings.HasSuffix(strings.ToLower(r[j+1]), ".sample"):
			return gitNone
		default:
			return gitHook
		}
	}
	return classifyGitLeaf(r[len(r)-1])
}

func classifyGitLeaf(leaf string) gitPathKind {
	switch {
	case foldAny(leaf, "config", "config.worktree"):
		return gitConfig
	case foldAny(leaf, "commondir"):
		return gitPointer
	case foldAny(leaf, "hooks"):
		return gitHooksRoot
	default:
		return gitNone
	}
}

func foldAny(s string, names ...string) bool {
	for _, n := range names {
		if strings.EqualFold(s, n) {
			return true
		}
	}
	return false
}

// Rename decides a rename onto a guarded path and, on allow, performs it.
func (g *GitGuard) Rename(ctx context.Context, r GuardRename) TrapResponse {
	target := OpWrite + " " + r.Dst
	if r.Flags&(renameExchange|renameWhiteout) != 0 {
		return g.refuse(r)
	}
	uid, gid, err := r.Tracee.Creds()
	if err != nil {
		return g.refuse(r)
	}
	snap, err := g.FS.Snapshot(r.Src, r.Dst, uid, gid)
	if err != nil {
		return g.fail(r, target, err)
	}
	if !reviewableText(snap.Data) || !reviewableText(snap.Old) {
		return g.refuse(r)
	}

	kind := g.classify(r.Dst)
	if kind == gitConfig && safeGitConfigChange(snap.Old, snap.Data) {
		g.audit(r.PID, r.ChannelID, target, string(types.DecisionAllow), "", map[string]string{"reason": "safe-keys"})
		return g.install(r, target, snap)
	}

	if g.Approver == nil {
		return g.refuse(r)
	}
	sum := sha256.Sum256(snap.Data)
	out := g.Approver.Request(ctx, r.ChannelID, ApprovalRequest{
		Kind:     "file",
		Target:   target,
		Source:   sourceForPID(r.PID, g.PeerSource),
		Message:  gitGuardMessage(kind),
		CacheKey: "file:git:" + r.Dst + ":" + hex.EncodeToString(sum[:]),
		Details:  map[string]string{"diff": gitGuardDiff(snap)},
		OnPrompt: func() {
			g.write(AuditEntry{Ts: g.now(), Channel: r.ChannelID, PID: r.PID, Kind: "file", Target: target, RuleID: gitGuardRuleID, Event: "request"})
		},
	})
	g.audit(r.PID, r.ChannelID, target, string(out.Decision), out.Actor, nil)
	if out.Decision != types.DecisionAllow {
		return denyResp(r.TrapID, syscall.EPERM)
	}
	return g.install(r, target, snap)
}

func (g *GitGuard) install(r GuardRename, target string, snap *GuardSnapshot) TrapResponse {
	if err := g.FS.Install(snap, r.Flags&renameNoReplace != 0); err != nil {
		return g.fail(r, target, err)
	}
	return performedResp(r.TrapID)
}

func (g *GitGuard) refuse(r GuardRename) TrapResponse {
	return g.Refuse(r.TrapID, r.PID, r.ChannelID, OpWrite, r.Dst)
}

// fail maps a filesystem error to the errno the tracee would have seen
// (ENOENT, EEXIST, EACCES, …). Anything else — including a source that
// isn't a regular file or is too big to show — is EPERM.
func (g *GitGuard) fail(r GuardRename, target string, err error) TrapResponse {
	g.audit(r.PID, r.ChannelID, target, string(types.DecisionDeny), "", map[string]string{"error": err.Error()})
	var errno syscall.Errno
	if errors.As(err, &errno) && errno != 0 {
		return denyResp(r.TrapID, errno)
	}
	return denyResp(r.TrapID, syscall.EPERM)
}

func (g *GitGuard) audit(pid int, channelID, target, decision, actor string, extra map[string]string) {
	g.write(AuditEntry{
		Ts:          g.now(),
		Channel:     channelID,
		PID:         pid,
		Kind:        "file",
		Target:      target,
		RuleID:      gitGuardRuleID,
		Decision:    decision,
		PromptedWho: actor,
		Extra:       extra,
	})
}

func (g *GitGuard) write(e AuditEntry) {
	if g.Auditor != nil {
		g.Auditor.Write(e)
	}
}

func (g *GitGuard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func gitGuardMessage(kind gitPathKind) string {
	switch kind {
	case gitConfig:
		return "git config change — review the diff"
	case gitHook, gitHooksRoot:
		return "git hook write — review the script"
	default:
		return "git dir pointer write — review the target"
	}
}

// gitGuardDiff renders the change as a unified diff of the current target
// against the bytes the gate will write.
func gitGuardDiff(snap *GuardSnapshot) string {
	from := snap.Dst + " (current)"
	if !snap.OldExists {
		from = "/dev/null"
	}
	diff, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(snap.Old)),
		B:        difflib.SplitLines(string(snap.Data)),
		FromFile: from,
		ToFile:   snap.Dst,
		Context:  3,
	})
	if diff == "" {
		return "(no change)"
	}
	return diff
}

// reviewableText reports whether b can be shown on a card as-is: valid
// UTF-8, no control characters but tab and newline, and no format
// characters (bidi overrides, zero-width joiners) that could make the
// card show something other than what git would read.
func reviewableText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		switch {
		case r == '\t' || r == '\n':
		case r < 0x20 || r == 0x7f || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r):
			return false
		}
	}
	return true
}

// gitConfigEntry is one key = value from a config file. Section and key
// are lowercased (git compares them case-insensitively); the subsection
// keeps its case.
type gitConfigEntry struct {
	section, sub, key, value string
}

var (
	gitConfigHeaderRe = regexp.MustCompile(`^\[([A-Za-z0-9-]+)(?:[ \t]+"([^"\\]*)")?\]$`)
	gitConfigKeyRe    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*)(?:[ \t]*=[ \t]*(.*))?$`)
)

// parseGitConfigStrict parses the plain subset of git's config syntax that
// git itself writes for simple values: [section] and [section "sub"]
// headers on their own line, and key = value lines whose value has no
// quotes, backslashes or comment characters. Anything outside that subset
// — continuation lines, escapes, a key on the header line, the old
// [section.sub] form — returns ok=false, so the caller asks rather than
// risk reading the file differently from git.
func parseGitConfigStrict(b []byte) ([]gitConfigEntry, bool) {
	var out []gitConfigEntry
	section, sub := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.Trim(line, " \t")
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			m := gitConfigHeaderRe.FindStringSubmatch(line)
			if m == nil {
				return nil, false
			}
			section, sub = strings.ToLower(m[1]), m[2]
			continue
		}
		m := gitConfigKeyRe.FindStringSubmatch(line)
		if m == nil || section == "" || strings.ContainsAny(m[2], `"\;#`) {
			return nil, false
		}
		out = append(out, gitConfigEntry{section: section, sub: sub, key: strings.ToLower(m[1]), value: m[2]})
	}
	return out, true
}

// safeGitConfigKeys are the entries git init, clone, remote add, fetch,
// branch --set-upstream-to, push -u and `git config user.*` write. None of
// them makes git run a program. "*" stands for any subsection; "" for none.
var safeGitConfigKeys = map[string]map[string]bool{
	"core": {"repositoryformatversion": true, "filemode": true, "bare": true, "logallrefupdates": true,
		"ignorecase": true, "precomposeunicode": true, "symlinks": true},
	"extensions": {"objectformat": true},
	"user":       {"name": true, "email": true},
	"remote.*":   {"url": true, "pushurl": true, "fetch": true},
	"branch.*":   {"remote": true, "merge": true},
	"submodule.*": {
		"url": true, "active": true,
	},
	"init": {"defaultbranch": true},
	"push": {"default": true, "autosetupremote": true},
	"pull": {"rebase": true},
}

// safeGitURLRe accepts network URLs git fetches over its own transports
// (https, ssh, git, and scp-like user@host:path). Local paths, file://,
// and transport helpers (ext::, fd::, <helper>::) ask instead.
var safeGitURLRe = regexp.MustCompile(`^(?:(?:https?|ssh|git)://[A-Za-z0-9][^\s]*|[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[^\s]+)$`)

func safeGitConfigEntry(e gitConfigEntry) bool {
	section := e.section
	if e.sub != "" {
		section += ".*"
	}
	if !safeGitConfigKeys[section][e.key] {
		return false
	}
	if e.key == "url" || e.key == "pushurl" {
		return safeGitURLRe.MatchString(e.value)
	}
	return true
}

// safeGitConfigChange reports whether turning old into new only adds or
// changes entries from safeGitConfigKeys. Removing an entry never makes git
// run anything, so removals don't count. Either file outside the strict
// subset means "not safe".
func safeGitConfigChange(old, new []byte) bool {
	before, ok := parseGitConfigStrict(old)
	if !ok {
		return false
	}
	after, ok := parseGitConfigStrict(new)
	if !ok {
		return false
	}
	have := make(map[gitConfigEntry]int, len(before))
	for _, e := range before {
		have[e]++
	}
	for _, e := range after {
		if have[e] > 0 {
			have[e]--
			continue
		}
		if !safeGitConfigEntry(e) {
			return false
		}
	}
	return true
}
