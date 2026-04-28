package dockerproxy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type BodyRuleSuite struct {
	suite.Suite
}

func TestBodyRuleSuite(t *testing.T) {
	suite.Run(t, new(BodyRuleSuite))
}

// compileOne is a tiny helper so each test can express a rule without the
// ceremony of CompilePolicy + multiple rule types.
func (s *BodyRuleSuite) compileOne(check types.JSONCheck) compiledJSONCheck {
	c, err := compileJSONCheck(check)
	require.NoError(s.T(), err)
	return c
}

// --- JSONPath walk ---

func (s *BodyRuleSuite) TestWalkFieldAccess() {
	c := s.compileOne(types.JSONCheck{Path: "a.b", Op: "equals", Values: []string{"hi"}})
	require.True(s.T(), c.match(map[string]any{"a": map[string]any{"b": "hi"}}))
	require.False(s.T(), c.match(map[string]any{"a": map[string]any{"b": "bye"}}))
}

func (s *BodyRuleSuite) TestWalkMissingFieldNoMatch() {
	c := s.compileOne(types.JSONCheck{Path: "a.missing", Op: "present"})
	require.False(s.T(), c.match(map[string]any{"a": map[string]any{}}))
}

func (s *BodyRuleSuite) TestWalkNonMapNoMatch() {
	c := s.compileOne(types.JSONCheck{Path: "a.b", Op: "present"})
	require.False(s.T(), c.match(map[string]any{"a": "not-a-map"}))
}

func (s *BodyRuleSuite) TestWalkWildcardArray() {
	c := s.compileOne(types.JSONCheck{Path: "arr[*]", Op: "equals", Values: []string{"two"}})
	require.True(s.T(), c.match(map[string]any{"arr": []any{"one", "two", "three"}}))
	require.False(s.T(), c.match(map[string]any{"arr": []any{"one"}}))
}

func (s *BodyRuleSuite) TestWalkWildcardNonArrayNoMatch() {
	c := s.compileOne(types.JSONCheck{Path: "arr[*]", Op: "equals", Values: []string{"x"}})
	require.False(s.T(), c.match(map[string]any{"arr": "not-array"}))
}

func (s *BodyRuleSuite) TestWalkWildcardThenField() {
	c := s.compileOne(types.JSONCheck{Path: "mounts[*].Source", Op: "starts_with_any", Values: []string{"/etc"}})
	body := map[string]any{"mounts": []any{
		map[string]any{"Source": "/workdir"},
		map[string]any{"Source": "/etc/passwd"},
	}}
	require.True(s.T(), c.match(body))
}

// --- Ops ---

func (s *BodyRuleSuite) TestOpPresentOnString() {
	c := s.compileOne(types.JSONCheck{Path: "x", Op: "present"})
	require.True(s.T(), c.match(map[string]any{"x": "hi"}))
	require.False(s.T(), c.match(map[string]any{"x": ""}))
}

func (s *BodyRuleSuite) TestOpPresentOnArray() {
	c := s.compileOne(types.JSONCheck{Path: "devices[*]", Op: "present"})
	require.True(s.T(), c.match(map[string]any{"devices": []any{"foo"}}))
	require.False(s.T(), c.match(map[string]any{"devices": []any{}}))
}

func (s *BodyRuleSuite) TestOpPresentOnNilNoMatch() {
	c := s.compileOne(types.JSONCheck{Path: "x", Op: "present"})
	require.False(s.T(), c.match(map[string]any{"x": nil}))
}

func (s *BodyRuleSuite) TestOpEmptyArray() {
	c := s.compileOne(types.JSONCheck{Path: "MaskedPaths", Op: "empty_array"})
	require.True(s.T(), c.match(map[string]any{"MaskedPaths": []any{}}))
	require.False(s.T(), c.match(map[string]any{"MaskedPaths": []any{"/proc/kcore"}}))
	require.False(s.T(), c.match(map[string]any{"MaskedPaths": nil}))
}

func (s *BodyRuleSuite) TestOpEqualsOnBool() {
	c := s.compileOne(types.JSONCheck{Path: "Privileged", Op: "equals", Values: []string{"true"}})
	require.True(s.T(), c.match(map[string]any{"Privileged": true}))
	require.False(s.T(), c.match(map[string]any{"Privileged": false}))
}

func (s *BodyRuleSuite) TestOpEqualsBoolFalseMatchesLiteral() {
	c := s.compileOne(types.JSONCheck{Path: "Privileged", Op: "equals", Values: []string{"false"}})
	require.True(s.T(), c.match(map[string]any{"Privileged": false}))
}

func (s *BodyRuleSuite) TestOpContainsAny() {
	c := s.compileOne(types.JSONCheck{Path: "CapAdd[*]", Op: "contains_any", Values: []string{"SYS_ADMIN", "SYS_PTRACE"}})
	require.True(s.T(), c.match(map[string]any{"CapAdd": []any{"NET_RAW", "SYS_PTRACE"}}))
	require.False(s.T(), c.match(map[string]any{"CapAdd": []any{"NET_RAW"}}))
}

func (s *BodyRuleSuite) TestOpStartsWithAny() {
	c := s.compileOne(types.JSONCheck{Path: "Source", Op: "starts_with_any", Values: []string{"/etc", "/root"}})
	require.True(s.T(), c.match(map[string]any{"Source": "/etc/passwd"}))
	require.True(s.T(), c.match(map[string]any{"Source": "/rootfs"})) // prefix-only by design
	require.False(s.T(), c.match(map[string]any{"Source": "/workdir"}))
}

func (s *BodyRuleSuite) TestOpSourcePathInBind() {
	// Bind format: "src:dst[:mode]" — regex evaluates against the src side.
	c := s.compileOne(types.JSONCheck{
		Path: "Binds[*]", Op: "source_path_in",
		Values: []string{`^/$`, `^/etc(/|$)`, `^/var/run/docker\.sock$`},
	})
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/:/host"}}))
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/etc:/x"}}))
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/var/run/docker.sock:/var/run/docker.sock:rw"}}))
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/project:/app"}}))
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"/etcd:/x"}})) // not /etc
}

func (s *BodyRuleSuite) TestOpSourcePathInNoColonDenies() {
	// Malformed Bind with no colon — still checked against the regex list.
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}})
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/"}}))
}

func (s *BodyRuleSuite) TestOpSourcePathInEmptyValueSkipped() {
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}})
	require.False(s.T(), c.match(map[string]any{"Binds": []any{""}}))
	require.False(s.T(), c.match(map[string]any{"Binds": []any{":only-target"}}))
}

func (s *BodyRuleSuite) TestLeafArrayUnwrap() {
	// path "arr" lands on a []any; equals op should still match any element.
	c := s.compileOne(types.JSONCheck{Path: "arr", Op: "equals", Values: []string{"x"}})
	require.True(s.T(), c.match(map[string]any{"arr": []any{"y", "x"}}))
	require.False(s.T(), c.match(map[string]any{"arr": []any{"y"}}))
}

func (s *BodyRuleSuite) TestExtractSourcePath() {
	require.Equal(s.T(), "/src", extractSourcePath("/src:/dst"))
	require.Equal(s.T(), "/src", extractSourcePath("/src:/dst:ro"))
	require.Equal(s.T(), "/only", extractSourcePath("/only"))
	require.Empty(s.T(), extractSourcePath(""))
	require.Empty(s.T(), extractSourcePath(":/dst"))
}

func (s *BodyRuleSuite) TestIsPresent() {
	require.True(s.T(), isPresent("x"))
	require.False(s.T(), isPresent(""))
	require.True(s.T(), isPresent([]any{"a"}))
	require.False(s.T(), isPresent([]any{}))
	require.True(s.T(), isPresent(map[string]any{"k": 1}))
	require.False(s.T(), isPresent(map[string]any{}))
	require.False(s.T(), isPresent(nil))
	require.True(s.T(), isPresent(42))
	require.True(s.T(), isPresent(true))
}

// --- Symlink resolution for source_path_in ---

func (s *BodyRuleSuite) TestSourcePathInResolvedSymlinkMatchesDeny() {
	// Agent submits "/workdir/link:/host" where /workdir/link → /. The literal
	// source doesn't match `^/$` but the resolved one does — must fire.
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}})
	c.parentDecision = types.DecisionDeny
	c.resolveSymlinks = func(p string) (string, error) {
		require.Equal(s.T(), "/workdir/link", p)
		return "/", nil
	}
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/link:/host"}}))
}

func (s *BodyRuleSuite) TestSourcePathInResolvedSymlinkOutsideRuleSet() {
	// Symlink resolves to a path that doesn't match any deny pattern — allow.
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/etc(/|$)`}})
	c.parentDecision = types.DecisionDeny
	c.resolveSymlinks = func(string) (string, error) {
		return "/workdir/project/build", nil
	}
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/link:/app"}}))
}

func (s *BodyRuleSuite) TestSourcePathInLiteralMatchSkipsResolver() {
	// Literal source already matches the deny pattern — resolver must not be
	// invoked (unnecessary syscall + the path may not exist for resolve to
	// return cleanly).
	called := false
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}})
	c.parentDecision = types.DecisionDeny
	c.resolveSymlinks = func(string) (string, error) {
		called = true
		return "", errors.New("must not be called")
	}
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/:/host"}}))
	require.False(s.T(), called)
}

func (s *BodyRuleSuite) TestSourcePathInResolveFailureFiresDeny() {
	// Broken symlink (or any resolve error) — fire the rule when it's a deny.
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/etc(/|$)`}})
	c.parentDecision = types.DecisionDeny
	c.resolveSymlinks = func(string) (string, error) {
		return "", errors.New("no such file")
	}
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/missing:/x"}}))
}

func (s *BodyRuleSuite) TestSourcePathInNamedVolumeSkipsResolver() {
	// Compose v2 sends short-syntax binds (incl. named volumes) via
	// HostConfig.Binds. A named-volume entry like "myvol:/target:rw" extracts
	// to source "myvol" — not a host path. Must not invoke the resolver and
	// must not fire the deny via the resolve-failure branch.
	called := false
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/etc(/|$)`}})
	c.parentDecision = types.DecisionDeny
	c.resolveSymlinks = func(string) (string, error) {
		called = true
		return "", errors.New("must not be called for volume name")
	}
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"myvol:/target:rw"}}))
	require.False(s.T(), called)
}

func (s *BodyRuleSuite) TestSourcePathInResolveFailureDoesNotFireAllow() {
	// Allow rules with source_path_in must NOT fire on resolve failure —
	// otherwise a missing path would be auto-allowed.
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/etc(/|$)`}})
	c.parentDecision = types.DecisionAllow
	c.resolveSymlinks = func(string) (string, error) {
		return "", errors.New("eaccess")
	}
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/link:/x"}}))
}

func (s *BodyRuleSuite) TestSourcePathInNoResolverFallsBackToLiteral() {
	// nil resolver = current (pre-fix) behaviour: only the literal source
	// string is matched. A symlink would not be unmasked.
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}})
	c.parentDecision = types.DecisionDeny
	require.Nil(s.T(), c.resolveSymlinks)
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/link:/host"}}))
	require.True(s.T(), c.match(map[string]any{"Binds": []any{"/:/host"}}))
}

func (s *BodyRuleSuite) TestSourcePathInResolverIdempotentSkipsRecheck() {
	// EvalSymlinks of a path that has no symlinks returns the input
	// unchanged — the second loop is skipped (covers the resolved==src
	// fast-path).
	calls := 0
	c := s.compileOne(types.JSONCheck{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/etc(/|$)`}})
	c.parentDecision = types.DecisionDeny
	c.resolveSymlinks = func(p string) (string, error) {
		calls++
		return p, nil
	}
	require.False(s.T(), c.match(map[string]any{"Binds": []any{"/workdir/project:/app"}}))
	require.Equal(s.T(), 1, calls)
}

// --- Unknown op (should never reach match since compile rejects, but defense-in-depth) ---

func (s *BodyRuleSuite) TestEvalUnknownOpReturnsFalse() {
	c := compiledJSONCheck{segments: []pathSegment{{name: "x"}}, op: "made-up"}
	require.False(s.T(), c.match(map[string]any{"x": "anything"}))
}
