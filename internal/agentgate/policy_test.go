package agentgate

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type PolicySuite struct {
	suite.Suite
}

func TestPolicySuite(t *testing.T) {
	suite.Run(t, new(PolicySuite))
}

// --- Compilation ---

func (s *PolicySuite) TestCompileEmptyPolicy() {
	p, err := CompilePolicy(types.DecisionAllow, nil, nil, nil)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), p)
	require.Equal(s.T(), types.DecisionAllow, p.MatchPath("/x").Decision)
	require.Equal(s.T(), types.DecisionAllow, p.MatchCommand("/bin/true", nil).Decision)
	require.Equal(s.T(), types.DecisionAllow, p.MatchFile("read", "/x").Decision)
}

func (s *PolicySuite) TestCompileUnknownDefaultFallsBackToAllow() {
	p, err := CompilePolicy(types.Decision("garbage"), nil, nil, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionAllow, p.MatchPath("/x").Decision)
}

func (s *PolicySuite) TestCompileRejectsEmptyPathPattern() {
	_, err := CompilePolicy(types.DecisionAllow,
		[]types.PathRule{{Pattern: "", Decision: types.DecisionAllow}}, nil, nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "pattern is required")
}

func (s *PolicySuite) TestCompileRejectsInvalidDecisionOnPathRule() {
	_, err := CompilePolicy(types.DecisionAllow,
		[]types.PathRule{{Pattern: "/x", Decision: types.Decision("maybe")}}, nil, nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unknown decision")
}

func (s *PolicySuite) TestCompileRejectsInvalidDecisionOnCommandRule() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{{Decision: types.Decision("maybe")}}, nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "command_rules[0]")
}

func (s *PolicySuite) TestCompileRejectsInvalidDecisionOnFileRule() {
	_, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{{Decision: types.Decision("maybe")}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "file_rules[0]")
}

func (s *PolicySuite) TestCompileRejectsBadRegex() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{{Commands: []string{"x"}, ArgsPatterns: []string{"["}, Decision: types.DecisionDeny}},
		nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "args_patterns[0]")
}

func (s *PolicySuite) TestCompileRejectsUnknownFileOp() {
	_, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{{Paths: []string{"/x"}, Operations: []string{"nope"}, Decision: types.DecisionDeny}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), `unknown operation "nope"`)
}

func (s *PolicySuite) TestCompileRejectsBadGlob() {
	_, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{{Paths: []string{"[invalid"}, Decision: types.DecisionDeny}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "file_rules[0].paths[0]")
}

// --- Path matching ---

func (s *PolicySuite) TestMatchPathFirstMatchWins() {
	p, err := CompilePolicy(types.DecisionAllow,
		[]types.PathRule{
			{Pattern: "/var/run/docker.sock", Decision: types.DecisionApprove, Message: "docker"},
			{Pattern: "/var/run/docker.sock", Decision: types.DecisionDeny, Message: "unreachable"},
		}, nil, nil)
	require.NoError(s.T(), err)
	r := p.MatchPath("/var/run/docker.sock")
	require.Equal(s.T(), types.DecisionApprove, r.Decision)
	require.Equal(s.T(), "docker", r.Message)
	require.Equal(s.T(), "path[0]", r.RuleID)
}

func (s *PolicySuite) TestMatchPathFallsThroughToDefault() {
	p, err := CompilePolicy(types.DecisionDeny,
		[]types.PathRule{{Pattern: "/other", Decision: types.DecisionAllow}}, nil, nil)
	require.NoError(s.T(), err)
	r := p.MatchPath("/var/run/docker.sock")
	require.Equal(s.T(), types.DecisionDeny, r.Decision)
	require.Equal(s.T(), "default", r.RuleID)
}

// --- Command matching ---

func (s *PolicySuite) TestMatchCommandBasenameExtracted() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{
			{Commands: []string{"rm"}, ArgsPatterns: []string{`-rf /`}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	r := p.MatchCommand("/usr/bin/rm", []string{"-rf", "/tmp/test"})
	require.Equal(s.T(), types.DecisionDeny, r.Decision)
	require.Equal(s.T(), "cmd[0]", r.RuleID)
}

func (s *PolicySuite) TestMatchCommandRegexMustMatchArgs() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{
			{Commands: []string{"rm"}, ArgsPatterns: []string{`^-rf /`}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	// No leading "-rf" → does not match, fall through to default allow.
	r := p.MatchCommand("/bin/rm", []string{"file.txt"})
	require.Equal(s.T(), types.DecisionAllow, r.Decision)
	require.Equal(s.T(), "default", r.RuleID)
}

func (s *PolicySuite) TestMatchCommandEmptyCommandsIsWildcard() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{
			{ArgsPatterns: []string{`^evil`}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	r := p.MatchCommand("/bin/any", []string{"evil", "payload"})
	require.Equal(s.T(), types.DecisionDeny, r.Decision)
}

func (s *PolicySuite) TestMatchCommandEmptyArgsPatternsIsWildcard() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{{Commands: []string{"git"}, Decision: types.DecisionApprove}}, nil)
	require.NoError(s.T(), err)
	r := p.MatchCommand("/usr/bin/git", []string{"status"})
	require.Equal(s.T(), types.DecisionApprove, r.Decision)
}

func (s *PolicySuite) TestMatchCommandBasenameMismatchSkipsRule() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.CommandRule{
			{Commands: []string{"docker"}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	r := p.MatchCommand("/usr/bin/ls", nil)
	require.Equal(s.T(), types.DecisionAllow, r.Decision)
	require.Equal(s.T(), "default", r.RuleID)
}

// --- File matching ---

func (s *PolicySuite) TestMatchFileDoublestarPath() {
	p, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{
			{Paths: []string{"**/.ssh/**"}, Operations: []string{"read"}, Decision: types.DecisionDeny, Message: "creds"},
		})
	require.NoError(s.T(), err)
	r := p.MatchFile("read", "/home/agent/.ssh/id_rsa")
	require.Equal(s.T(), types.DecisionDeny, r.Decision)
	require.Equal(s.T(), "creds", r.Message)
	require.Equal(s.T(), "file[0]", r.RuleID)
}

func (s *PolicySuite) TestMatchFileOpFilter() {
	p, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{
			{Paths: []string{"/etc/**"}, Operations: []string{"write"}, Decision: types.DecisionDeny},
		})
	require.NoError(s.T(), err)
	// read → op doesn't match, fall through to default.
	require.Equal(s.T(), types.DecisionAllow, p.MatchFile("read", "/etc/hosts").Decision)
	// write → op matches, deny.
	require.Equal(s.T(), types.DecisionDeny, p.MatchFile("write", "/etc/hosts").Decision)
}

func (s *PolicySuite) TestMatchFileEmptyOpsMatchesAll() {
	p, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{{Paths: []string{"/sys/**"}, Decision: types.DecisionDeny}})
	require.NoError(s.T(), err)
	for _, op := range []string{"read", "write", "create", "stat"} {
		require.Equal(s.T(), types.DecisionDeny, p.MatchFile(op, "/sys/fs/cgroup").Decision)
	}
}

func (s *PolicySuite) TestMatchFileEmptyPathsMatchesAll() {
	p, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{{Operations: []string{"write"}, Decision: types.DecisionDeny}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionDeny, p.MatchFile("write", "/anywhere/at/all").Decision)
}

func (s *PolicySuite) TestMatchFileFirstMatchWins() {
	p, err := CompilePolicy(types.DecisionAllow, nil, nil,
		[]types.FileRule{
			{Paths: []string{"/proc/*/mem"}, Operations: []string{"read"}, Decision: types.DecisionDeny, Message: "memory"},
			{Paths: []string{"/proc/**"}, Operations: []string{"read"}, Decision: types.DecisionAllow, Message: "proc-read"},
		})
	require.NoError(s.T(), err)
	r := p.MatchFile("read", "/proc/1/mem")
	require.Equal(s.T(), types.DecisionDeny, r.Decision)
	require.Equal(s.T(), "memory", r.Message)
}

func (s *PolicySuite) TestMatchFileFallsThroughToDefault() {
	p, err := CompilePolicy(types.DecisionApprove, nil, nil,
		[]types.FileRule{{Paths: []string{"/work/**"}, Operations: []string{"write"}, Decision: types.DecisionAllow}})
	require.NoError(s.T(), err)
	r := p.MatchFile("write", "/srv/secrets")
	require.Equal(s.T(), types.DecisionApprove, r.Decision)
	require.Equal(s.T(), "default", r.RuleID)
}

// --- Helper edge cases ---

func (s *PolicySuite) TestMatchAnyGlobSkipsBadPattern() {
	// A malformed glob (unmatched [ ) shouldn't crash; it should be skipped.
	require.False(s.T(), matchAnyGlob("foo", []string{"["}))
	require.True(s.T(), matchAnyGlob("foo", nil))
}

func (s *PolicySuite) TestMatchAnyDoublestarSkipsBadPattern() {
	require.False(s.T(), matchAnyDoublestar("/x", []string{"[bad"}))
	require.True(s.T(), matchAnyDoublestar("/x", nil))
}
