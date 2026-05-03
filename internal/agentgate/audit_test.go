package agentgate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AuditSuite struct {
	suite.Suite
	dir string
}

func TestAuditSuite(t *testing.T) {
	suite.Run(t, new(AuditSuite))
}

func (s *AuditSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

// --- Nop ---

func (s *AuditSuite) TestNopAuditorDiscards() {
	var a Auditor = NopAuditor{}
	a.Write(AuditEntry{Kind: "x"})
}

// --- Multi ---

type collectAuditor struct{ entries []AuditEntry }

func (c *collectAuditor) Write(e AuditEntry) { c.entries = append(c.entries, e) }

func (s *AuditSuite) TestMultiAuditorFansOut() {
	a, b := &collectAuditor{}, &collectAuditor{}
	m := NewMultiAuditor(a, b)
	e := AuditEntry{Kind: "connect", Target: "/x"}
	m.Write(e)
	require.Equal(s.T(), []AuditEntry{e}, a.entries)
	require.Equal(s.T(), []AuditEntry{e}, b.entries)
}

// --- File ---

func (s *AuditSuite) readLines(path string) []string {
	b, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func (s *AuditSuite) TestFileAuditorWritesJSONL() {
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	ts := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return ts }
	// Force rotation onto our fixed date.
	require.NoError(s.T(), a.rotate(ts))

	a.Write(AuditEntry{Kind: "execve", Target: "git push", Decision: "allow", RuleID: "cmd[0]"})
	a.Write(AuditEntry{Kind: "connect", Target: "/var/run/docker.sock", Decision: "approve", Extra: map[string]string{"cache": "miss"}})

	lines := s.readLines(filepath.Join(s.dir, "agentgate-2026-04-22.jsonl"))
	require.Len(s.T(), lines, 2)
	var e0 AuditEntry
	require.NoError(s.T(), json.Unmarshal([]byte(lines[0]), &e0))
	require.Equal(s.T(), "execve", e0.Kind)
	require.Equal(s.T(), ts, e0.Ts)
}

func (s *AuditSuite) TestFileAuditorFillsTimestamp() {
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	ts := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return ts }
	require.NoError(s.T(), a.rotate(ts))

	a.Write(AuditEntry{Kind: "x", Target: "y"})
	lines := s.readLines(filepath.Join(s.dir, "agentgate-2026-04-22.jsonl"))
	require.Len(s.T(), lines, 1)
	var e AuditEntry
	require.NoError(s.T(), json.Unmarshal([]byte(lines[0]), &e))
	require.Equal(s.T(), ts, e.Ts)
}

func (s *AuditSuite) TestFileAuditorRotatesOnDateChange() {
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	day1 := time.Date(2026, 4, 22, 23, 59, 0, 0, time.UTC)
	day2 := day1.Add(2 * time.Minute) // next UTC day

	a.now = func() time.Time { return day1 }
	require.NoError(s.T(), a.rotate(day1))
	a.Write(AuditEntry{Kind: "a", Target: "t"})

	a.now = func() time.Time { return day2 }
	a.Write(AuditEntry{Kind: "b", Target: "t"})

	day1Lines := s.readLines(filepath.Join(s.dir, "agentgate-2026-04-22.jsonl"))
	day2Lines := s.readLines(filepath.Join(s.dir, "agentgate-2026-04-23.jsonl"))
	require.Len(s.T(), day1Lines, 1)
	require.Len(s.T(), day2Lines, 1)
}

func (s *AuditSuite) TestFileAuditorRetentionPrunes() {
	// Pre-populate the dir with files spanning several days.
	for _, day := range []string{"2026-04-10", "2026-04-15", "2026-04-20", "2026-04-22"} {
		path := filepath.Join(s.dir, "agentgate-"+day+".jsonl")
		require.NoError(s.T(), os.WriteFile(path, []byte("{}\n"), 0o640))
	}
	// An unrelated file + a directory must not be touched.
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.dir, "README.md"), []byte("x"), 0o640))
	require.NoError(s.T(), os.Mkdir(filepath.Join(s.dir, "subdir"), 0o750))
	// A misnamed jsonl (agentgate-not-a-date.jsonl) must be left alone.
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.dir, "agentgate-not-a-date.jsonl"), []byte("{}\n"), 0o640))

	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })
	a.now = func() time.Time { return now }
	a.retentionDays = 5
	// Rotate to apply retention.
	require.NoError(s.T(), a.rotate(now))

	remaining, err := os.ReadDir(s.dir)
	require.NoError(s.T(), err)
	names := map[string]bool{}
	for _, e := range remaining {
		names[e.Name()] = true
	}
	// Older than 5 days (before 2026-04-17): pruned.
	require.False(s.T(), names["agentgate-2026-04-10.jsonl"])
	require.False(s.T(), names["agentgate-2026-04-15.jsonl"])
	// Within 5 days: kept.
	require.True(s.T(), names["agentgate-2026-04-20.jsonl"])
	require.True(s.T(), names["agentgate-2026-04-22.jsonl"])
	// Unrelated files / dirs / misnamed: kept.
	require.True(s.T(), names["README.md"])
	require.True(s.T(), names["subdir"])
	require.True(s.T(), names["agentgate-not-a-date.jsonl"])
}

func (s *AuditSuite) TestFileAuditorRetentionDisabled() {
	// Seed three day files; retentionDays=0 should keep all.
	for _, day := range []string{"2026-04-01", "2026-04-10", "2026-04-22"} {
		path := filepath.Join(s.dir, "agentgate-"+day+".jsonl")
		require.NoError(s.T(), os.WriteFile(path, []byte("{}\n"), 0o640))
	}
	a, err := NewFileAuditor(s.dir, 0, true) // disabled
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	remaining, err := os.ReadDir(s.dir)
	require.NoError(s.T(), err)
	names := []string{}
	for _, e := range remaining {
		names = append(names, e.Name())
	}
	// 3 seeded + today's file from constructor → 4 files present.
	require.GreaterOrEqual(s.T(), len(names), 3)
	found := 0
	for _, n := range names {
		if strings.HasPrefix(n, "agentgate-2026-04-") {
			found++
		}
	}
	require.GreaterOrEqual(s.T(), found, 3)
}

func (s *AuditSuite) TestFileAuditorClose() {
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	require.NoError(s.T(), a.Close())
	// Second close is idempotent.
	require.NoError(s.T(), a.Close())
	// Writes after close silently drop (file is nil).
	a.Write(AuditEntry{Kind: "x", Target: "y"})
}

func (s *AuditSuite) TestFileAuditorConstructorMkdirError() {
	// Create a file where the dir should be — MkdirAll fails.
	blockPath := filepath.Join(s.dir, "block")
	require.NoError(s.T(), os.WriteFile(blockPath, []byte("x"), 0o640))
	_, err := NewFileAuditor(filepath.Join(blockPath, "subdir"), 0, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mkdir audit dir")
}

func (s *AuditSuite) TestFileAuditorOpenError() {
	// Make the directory read-only so OpenFile (O_CREATE) fails. Read-only dirs
	// don't enforce as root, so root falls through to the directory-collision
	// path below: pre-creating today's audit path as a directory makes OpenFile
	// fail with EISDIR regardless of UID.
	ro := filepath.Join(s.dir, "ro")
	require.NoError(s.T(), os.Mkdir(ro, 0o500))
	s.T().Cleanup(func() { _ = os.Chmod(ro, 0o750) })
	if os.Geteuid() == 0 {
		today := time.Now().UTC().Format("2006-01-02")
		blocker := filepath.Join(ro, "agentgate-"+today+".jsonl")
		require.NoError(s.T(), os.Chmod(ro, 0o750))
		require.NoError(s.T(), os.Mkdir(blocker, 0o750))
		require.NoError(s.T(), os.Chmod(ro, 0o500))
	}
	_, err := NewFileAuditor(ro, 0, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "open audit file")
}

func (s *AuditSuite) TestFileAuditorRotationFallbackOnOpenError() {
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	// Swap the dir out from under the auditor so the next rotation's OpenFile fails.
	require.NoError(s.T(), a.Close())
	a.dir = filepath.Join(s.dir, "does-not-exist", "sub")

	ts := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return ts }
	// Should not panic; Write drops the entry silently.
	a.Write(AuditEntry{Kind: "x", Target: "y"})
}

func (s *AuditSuite) TestFileAuditorNonVerboseDropsSilentAllows() {
	// Default (verbose=false) behaviour: silent allow decisions must not
	// land on disk. Silent = Decision=="allow" AND PromptedWho=="". Denies
	// and user-clicked allows still pass through so every rejection and
	// every human-in-the-loop acknowledgement stays traceable.
	a, err := NewFileAuditor(s.dir, 0, false)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	ts := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return ts }
	require.NoError(s.T(), a.rotate(ts))

	a.Write(AuditEntry{Kind: "execve", Target: "ls", Decision: "allow"})                              // silent → dropped
	a.Write(AuditEntry{Kind: "execve", Target: "rm", Decision: "deny"})                               // silent deny → kept
	a.Write(AuditEntry{Kind: "execve", Target: "git commit", Decision: "allow", PromptedWho: "u-42"}) // user-allow → kept
	a.Write(AuditEntry{Kind: "execve", Target: "git push", Decision: "deny", PromptedWho: "u-42"})    // user-deny → kept

	lines := s.readLines(filepath.Join(s.dir, "agentgate-2026-04-22.jsonl"))
	require.Len(s.T(), lines, 3)

	targets := []string{}
	for _, ln := range lines {
		var e AuditEntry
		require.NoError(s.T(), json.Unmarshal([]byte(ln), &e))
		targets = append(targets, e.Target)
	}
	require.ElementsMatch(s.T(), []string{"rm", "git commit", "git push"}, targets)
}

func (s *AuditSuite) TestFileAuditorVerboseKeepsSilentAllows() {
	// Verbose=true: every decision is recorded, including silent allows,
	// so operators debugging rule authoring can see the full trace.
	a, err := NewFileAuditor(s.dir, 0, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })

	ts := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return ts }
	require.NoError(s.T(), a.rotate(ts))

	a.Write(AuditEntry{Kind: "execve", Target: "ls", Decision: "allow"})
	a.Write(AuditEntry{Kind: "connect", Target: "/x", Decision: "allow"})

	lines := s.readLines(filepath.Join(s.dir, "agentgate-2026-04-22.jsonl"))
	require.Len(s.T(), lines, 2)
}

func (s *AuditSuite) TestFileAuditorPruneDirReadError() {
	a, err := NewFileAuditor(s.dir, 7, true)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = a.Close() })
	// Remove the dir → pruneLocked's ReadDir returns error; no panic.
	require.NoError(s.T(), a.Close())
	require.NoError(s.T(), os.RemoveAll(s.dir))
	a.pruneLocked(time.Now())
}
