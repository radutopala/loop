package dockerproxy

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

func (s *PolicySuite) TestCompileEmptyPolicyDefaultsApprove() {
	p, err := CompilePolicy(types.Decision(""), nil, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionApprove, p.MatchHTTP("GET", "/anything").Decision)
}

func (s *PolicySuite) TestCompileUnknownDefaultFallsBackToApprove() {
	p, err := CompilePolicy(types.Decision("garbage"), nil, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionApprove, p.MatchHTTP("GET", "/anything").Decision)
}

func (s *PolicySuite) TestCompileRejectsInvalidHTTPDecision() {
	_, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{{Methods: []string{"GET"}, Paths: []string{"^/$"}, Decision: types.Decision("x")}},
		nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "http_rules[0]")
}

func (s *PolicySuite) TestCompileRejectsInvalidBodyDecision() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{AppliesTo: "POST ^/$", Decision: types.Decision("x")}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "body_rules[0]")
}

func (s *PolicySuite) TestCompileRejectsBadPathRegex() {
	_, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{{Methods: []string{"GET"}, Paths: []string{"["}, Decision: types.DecisionAllow}},
		nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "http_rules[0].paths[0]")
}

func (s *PolicySuite) TestCompileRejectsMalformedAppliesTo() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{AppliesTo: "NOTHING", Decision: types.DecisionDeny}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "applies_to")
}

func (s *PolicySuite) TestCompileRejectsEmptyBodyAppliesToPath() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{AppliesTo: "POST   ", Decision: types.DecisionDeny}})
	require.Error(s.T(), err)
}

func (s *PolicySuite) TestCompileRejectsBadAppliesToRegex() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{AppliesTo: "POST [", Decision: types.DecisionDeny}})
	require.Error(s.T(), err)
}

func (s *PolicySuite) TestCompileRejectsBadJSONCheckPath() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo: "POST ^/$",
			JSONChecks: []types.JSONCheck{
				{Path: "", Op: "equals", Values: []string{"x"}},
			},
			Decision: types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "json_checks[0]")
}

func (s *PolicySuite) TestCompileRejectsTrailingDotInJSONPath() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/$",
			JSONChecks: []types.JSONCheck{{Path: "a.", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "trailing dot")
}

func (s *PolicySuite) TestCompileRejectsBadBracketInJSONPath() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/$",
			JSONChecks: []types.JSONCheck{{Path: "a[0]", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expected [*]")
}

func (s *PolicySuite) TestCompileRejectsUnknownJSONCheckOp() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/$",
			JSONChecks: []types.JSONCheck{{Path: "a", Op: "noop", Values: []string{"x"}}},
			Decision:   types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unknown op")
}

func (s *PolicySuite) TestCompileRejectsEqualsWithoutValues() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/$",
			JSONChecks: []types.JSONCheck{{Path: "a", Op: "equals"}},
			Decision:   types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "requires at least one value")
}

func (s *PolicySuite) TestCompileRejectsBadSourcePathRegex() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo: "POST ^/$",
			JSONChecks: []types.JSONCheck{
				{Path: "Binds[*]", Op: "source_path_in", Values: []string{"["}},
			},
			Decision: types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "values[0] regex")
}

// --- MatchHTTP ---

func (s *PolicySuite) TestMatchHTTPFirstMatchWins() {
	p, err := CompilePolicy(types.DecisionApprove,
		[]types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/a$"}, Decision: types.DecisionAllow, Message: "a-allow"},
			{Methods: []string{"GET"}, Paths: []string{"^/a$"}, Decision: types.DecisionDeny, Message: "unreachable"},
		}, nil)
	require.NoError(s.T(), err)
	res := p.MatchHTTP("GET", "/a")
	require.Equal(s.T(), types.DecisionAllow, res.Decision)
	require.Equal(s.T(), "a-allow", res.Message)
	require.Equal(s.T(), "http[0]", res.RuleID)
}

func (s *PolicySuite) TestMatchHTTPMethodWildcard() {
	p, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"*"}, Paths: []string{"^/swarm/"}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionDeny, p.MatchHTTP("POST", "/swarm/join").Decision)
	require.Equal(s.T(), types.DecisionDeny, p.MatchHTTP("DELETE", "/swarm/init").Decision)
}

func (s *PolicySuite) TestMatchHTTPMethodCaseInsensitive() {
	p, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"post"}, Paths: []string{"^/x$"}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionDeny, p.MatchHTTP("POST", "/x").Decision)
	require.Equal(s.T(), types.DecisionDeny, p.MatchHTTP("post", "/x").Decision)
}

func (s *PolicySuite) TestMatchHTTPDefaultDecision() {
	p, err := CompilePolicy(types.DecisionApprove,
		[]types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/a$"}, Decision: types.DecisionAllow},
		}, nil)
	require.NoError(s.T(), err)
	res := p.MatchHTTP("POST", "/not-configured")
	require.Equal(s.T(), types.DecisionApprove, res.Decision)
	require.Equal(s.T(), "default", res.RuleID)
}

func (s *PolicySuite) TestMatchHTTPMethodMismatchFallsThrough() {
	p, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{
			{Methods: []string{"GET"}, Paths: []string{"^/a$"}, Decision: types.DecisionDeny},
		}, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionAllow, p.MatchHTTP("POST", "/a").Decision)
}

// --- MaxBodyBytes ---

func (s *PolicySuite) TestMaxBodyBytesZeroWhenNoRuleMatches() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo: "POST ^/containers/create$", MaxBodyBytes: 1024,
			JSONChecks: []types.JSONCheck{{Path: "x", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(1024), p.MaxBodyBytes("POST", "/containers/create"))
	require.Zero(s.T(), p.MaxBodyBytes("GET", "/containers/create"))
	require.Zero(s.T(), p.MaxBodyBytes("POST", "/images/create"))
}

func (s *PolicySuite) TestMaxBodyBytesPicksLargest() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{
			{
				AppliesTo: "POST ^/a$", MaxBodyBytes: 1024,
				JSONChecks: []types.JSONCheck{{Path: "x", Op: "present"}},
				Decision:   types.DecisionDeny,
			},
			{
				AppliesTo: "POST ^/a$", MaxBodyBytes: 4096,
				JSONChecks: []types.JSONCheck{{Path: "y", Op: "present"}},
				Decision:   types.DecisionDeny,
			},
		})
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(4096), p.MaxBodyBytes("POST", "/a"))
}

// --- CheckBody ---

func (s *PolicySuite) TestCheckBodyFiresPresent() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "HostConfig.Devices[*]", Op: "present"}},
			Decision:   types.DecisionDeny,
			Message:    "no devices",
		}})
	require.NoError(s.T(), err)
	body := map[string]any{"HostConfig": map[string]any{"Devices": []any{"foo"}}}
	res := p.CheckBody("POST", "/x", "application/json", body)
	require.True(s.T(), res.Fired)
	require.Equal(s.T(), types.DecisionDeny, res.Decision)
	require.Equal(s.T(), "no devices", res.Message)
	require.Equal(s.T(), "body[0]", res.RuleID)
}

func (s *PolicySuite) TestCheckBodyDoesNotFireOnMissingField() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "HostConfig.Privileged", Op: "equals", Values: []string{"true"}}},
			Decision:   types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	res := p.CheckBody("POST", "/x", "application/json", map[string]any{"HostConfig": map[string]any{}})
	require.False(s.T(), res.Fired)
}

func (s *PolicySuite) TestCheckBodySkipsContentTypeMismatch() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:    "POST ^/x$",
			ContentTypes: []string{"application/json"},
			JSONChecks:   []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	res := p.CheckBody("POST", "/x", "application/x-tar", map[string]any{"a": "x"})
	require.False(s.T(), res.Fired)
	require.Equal(s.T(), "content-type-mismatch", res.Skipped)
}

func (s *PolicySuite) TestCheckBodyContentTypeStripsParams() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:    "POST ^/x$",
			ContentTypes: []string{"application/json"},
			JSONChecks:   []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:     types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	res := p.CheckBody("POST", "/x", "application/json; charset=utf-8", map[string]any{"a": "x"})
	require.True(s.T(), res.Fired)
}

func (s *PolicySuite) TestCheckBodyMethodMismatchNoFire() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	res := p.CheckBody("GET", "/x", "application/json", map[string]any{"a": "x"})
	require.False(s.T(), res.Fired)
}

func (s *PolicySuite) TestCheckBodyNilBodyNoFire() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	res := p.CheckBody("POST", "/x", "application/json", nil)
	require.False(s.T(), res.Fired)
}

// Method matches but path doesn't — covers the path-mismatch continue branch
// inside CheckBody.
func (s *PolicySuite) TestCheckBodyPathMismatchSkipsRule() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "a", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	res := p.CheckBody("POST", "/y", "application/json", map[string]any{"a": "x"})
	require.False(s.T(), res.Fired)
}

// Methods omitted on an HTTP rule means "any method" — covers compileMethods's
// len==0 branch.
func (s *PolicySuite) TestMatchHTTPNoMethodsMeansAny() {
	p, err := CompilePolicy(types.DecisionAllow,
		[]types.HTTPServiceRule{{Paths: []string{"^/$"}, Decision: types.DecisionDeny, Message: "m"}},
		nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), types.DecisionDeny, p.MatchHTTP("GET", "/").Decision)
	require.Equal(s.T(), types.DecisionDeny, p.MatchHTTP("DELETE", "/").Decision)
}

// A char other than '.' or '[' after [*] triggers parseJSONPath's
// "expected '.' or '['" branch.
func (s *PolicySuite) TestCompileRejectsJunkAfterWildcard() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "a[*]x", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expected '.' or '['")
}

// SetSymlinkResolver stamps the resolver onto every source_path_in check across
// all body rules. Other ops are left alone (they don't consult the resolver).
func (s *PolicySuite) TestSetSymlinkResolverStampsSourcePathInChecksOnly() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo: "POST ^/containers/create$",
			JSONChecks: []types.JSONCheck{
				{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}},
				{Path: "Privileged", Op: "equals", Values: []string{"true"}},
			},
			Decision: types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	called := false
	p.SetSymlinkResolver(func(path string) (string, error) {
		called = true
		return path, nil
	})
	require.Equal(s.T(), types.DecisionDeny, p.bodyRules[0].checks[0].parentDecision)
	require.NotNil(s.T(), p.bodyRules[0].checks[0].resolveSymlinks)
	require.Nil(s.T(), p.bodyRules[0].checks[1].resolveSymlinks, "non-source_path_in op should not be stamped")

	// Sanity: invoking the stamped resolver works.
	_, _ = p.bodyRules[0].checks[0].resolveSymlinks("/x")
	require.True(s.T(), called)
}

// SetSymlinkResolver(nil) clears the resolver — opt-out for tests / config.
func (s *PolicySuite) TestSetSymlinkResolverNilClears() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo: "POST ^/containers/create$",
			JSONChecks: []types.JSONCheck{
				{Path: "Binds[*]", Op: "source_path_in", Values: []string{`^/$`}},
			},
			Decision: types.DecisionDeny,
		}})
	require.NoError(s.T(), err)
	p.SetSymlinkResolver(func(p string) (string, error) { return p, nil })
	require.NotNil(s.T(), p.bodyRules[0].checks[0].resolveSymlinks)
	p.SetSymlinkResolver(nil)
	require.Nil(s.T(), p.bodyRules[0].checks[0].resolveSymlinks)
}

// End-to-end through CheckBody: resolver unmasks a symlink to "/" so the deny
// rule fires even though the literal source is "/workdir/link".
func (s *PolicySuite) TestCheckBodyWithSymlinkResolverFires() {
	p, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo: "POST ^/containers/create$",
			JSONChecks: []types.JSONCheck{
				{Path: "HostConfig.Binds[*]", Op: "source_path_in", Values: []string{`^/$`}},
			},
			Decision: types.DecisionDeny,
			Message:  "host root bind blocked",
		}})
	require.NoError(s.T(), err)
	p.SetSymlinkResolver(func(string) (string, error) { return "/", nil })

	body := map[string]any{
		"HostConfig": map[string]any{
			"Binds": []any{"/workdir/link:/host"},
		},
	}
	res := p.CheckBody("POST", "/containers/create", "application/json", body)
	require.True(s.T(), res.Fired)
	require.Equal(s.T(), types.DecisionDeny, res.Decision)
}

// parseJSONPath rejects "a..b" — two consecutive dots produce an empty field
// at the second dot's offset.
func (s *PolicySuite) TestCompileRejectsDoubleDot() {
	_, err := CompilePolicy(types.DecisionAllow, nil,
		[]types.BodyRule{{
			AppliesTo:  "POST ^/x$",
			JSONChecks: []types.JSONCheck{{Path: "a..b", Op: "present"}},
			Decision:   types.DecisionDeny,
		}})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "empty field")
}
