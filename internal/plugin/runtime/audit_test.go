package runtime

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/require"
)

// writeAudit writes a day's audit file for cogtest in ch1.
func (s *ManagerSuite) writeAudit(name, content string) {
	dir := filepath.Join(s.m.cfg.Root, "cogtest", "audit", "ch1")
	require.NoError(s.T(), os.MkdirAll(dir, 0o750))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, name), []byte(content), 0o640))
}

func (s *ManagerSuite) TestAudit() {
	s.writeAudit("agentgate-2026-10-09.jsonl",
		`{"ts":"2026-10-09T10:00:00Z","kind":"exec","target":"/bin/sh","rule_id":"deny-shell","decision":"deny"}`+"\n"+
			"not json\n"+
			`{"ts":"2026-10-09T11:00:00Z","kind":"open","target":"/etc/shadow","rule_id":"default","decision":"deny"}`+"\n")
	// The day's last line isn't finished being written.
	s.writeAudit("agentgate-2026-10-10.jsonl",
		`{"ts":"2026-10-10T08:00:00Z","kind":"connect","target":"example.com:443","rule_id":"net","decision":"allow","prompted_who":"policy"}`+"\n"+
			`{"ts":"2026-10-10T09:00:00Z","kind":"exec","target":"/usr/bin/cu`)
	// A record longer than maxAuditLine is skipped, the next read.
	s.writeAudit("agentgate-2026-10-08.jsonl",
		`{"kind":"exec","target":"`+strings.Repeat("x", maxAuditLine)+`"}`+"\n"+
			`{"ts":"2026-10-08T10:00:00Z","kind":"exec","target":"/bin/curl","decision":"deny"}`+"\n")
	// Not a day's audit file.
	s.writeAudit("agentgate-latest.jsonl", `{"kind":"exec","target":"/x","decision":"deny"}`+"\n")
	s.writeAudit("notes.txt", `{"kind":"exec","target":"/x","decision":"deny"}`+"\n")
	require.NoError(s.T(), os.Mkdir(filepath.Join(s.m.cfg.Root, "cogtest", "audit", "ch1", "agentgate-2026-10-11.jsonl"), 0o750))

	got, err := s.m.Audit("cogtest", "ch1", 10)
	require.NoError(s.T(), err)
	var targets []string
	for _, e := range got {
		targets = append(targets, e.Target)
	}
	require.Equal(s.T(), []string{"example.com:443", "/etc/shadow", "/bin/sh", "/bin/curl"}, targets)
	require.Equal(s.T(), "deny", got[1].Decision)
	require.Equal(s.T(), "default", got[1].RuleID)

	got, err = s.m.Audit("cogtest", "ch1", 2)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "/etc/shadow", got[1].Target)
}

func (s *ManagerSuite) TestAuditNone() {
	got, err := s.m.Audit("cogtest", "ch1", 10)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), got)
	require.Empty(s.T(), got)
}

func (s *ManagerSuite) TestAuditErrors() {
	tests := []struct {
		name    string
		plugin  string
		channel string
		setup   func()
		err     string
	}{
		{name: "channel outside the audit dir", plugin: "cogtest", channel: "..", err: `invalid plugin "cogtest" or channel ".."`},
		{name: "channel with a slash", plugin: "cogtest", channel: "a/b", err: `invalid plugin "cogtest" or channel "a/b"`},
		{name: "no plugin", channel: "ch1", err: `invalid plugin "" or channel "ch1"`},
		{name: "plugin of a dot", plugin: ".", channel: "ch1", err: `invalid plugin "." or channel "ch1"`},
		{name: "plugin with a backslash", plugin: `a\b`, channel: "ch1", err: `invalid plugin "a\\b" or channel "ch1"`},
		{
			name: "audit dir is a file", plugin: "cogtest", channel: "ch1",
			setup: func() {
				require.NoError(s.T(), os.MkdirAll(filepath.Join(s.m.cfg.Root, "cogtest", "audit"), 0o750))
				require.NoError(s.T(), os.WriteFile(filepath.Join(s.m.cfg.Root, "cogtest", "audit", "ch1"), nil, 0o640))
			},
			err: "reading the plugin gate audit dir",
		},
		{
			name: "day unreadable", plugin: "cogtest", channel: "ch1",
			setup: func() {
				s.writeAudit("agentgate-2026-10-10.jsonl", "")
				s.m.open = func(string) (io.ReadCloser, error) { return nil, errors.New("denied") }
			},
			err: "opening the plugin gate audit: denied",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if tt.setup != nil {
				tt.setup()
			}
			_, err := s.m.Audit(tt.plugin, tt.channel, 10)
			require.ErrorContains(s.T(), err, tt.err)
		})
	}
}
