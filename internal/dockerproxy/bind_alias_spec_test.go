package dockerproxy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/types"
)

// BindAliasSpecSuite states what the default body rules promise about bind
// sources: every spelling of a forbidden host path — extra slashes, "." and
// ".." segments, a symlink to it or to one of its parents — is refused like
// the plain path, through both HostConfig.Binds and HostConfig.Mounts, with
// the resolver the proxy runs with (filepath.EvalSymlinks).
type BindAliasSpecSuite struct {
	suite.Suite
	policy *Policy
	dir    string
}

func TestBindAliasSpecSuite(t *testing.T) {
	suite.Run(t, new(BindAliasSpecSuite))
}

func (s *BindAliasSpecSuite) SetupTest() {
	p, err := CompilePolicy(types.DecisionAllow, nil, config.DefaultDockerProxyBodyRules())
	s.Require().NoError(err)
	p.SetSymlinkResolver(filepath.EvalSymlinks)
	s.policy = p

	dir, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	s.dir = dir
	s.Require().NoError(os.Symlink("/etc", filepath.Join(dir, "etc-link")))
	s.Require().NoError(os.Symlink("/", filepath.Join(dir, "root-link")))
	s.Require().NoError(os.Symlink("root-link", filepath.Join(dir, "chain-link")))
	s.Require().NoError(os.Mkdir(filepath.Join(dir, "work"), 0o755))
}

// verdicts returns whether the create body rules refuse src as a short-form
// bind and as a long-form mount.
func (s *BindAliasSpecSuite) verdicts(src string) (bind, mount bool) {
	check := func(hostConfig map[string]any) bool {
		res := s.policy.CheckBody("POST", "/containers/create", "application/json", map[string]any{"HostConfig": hostConfig})
		return res.Fired && res.Decision == types.DecisionDeny
	}
	return check(map[string]any{"Binds": []any{src + ":/mnt:ro"}}),
		check(map[string]any{"Mounts": []any{map[string]any{"Type": "bind", "Source": src, "Target": "/mnt"}}})
}

func (s *BindAliasSpecSuite) TestEverySpellingOfAForbiddenSourceIsRefused() {
	for _, src := range []string{
		"/", "//", "/.", "/./", "/..",
		"/etc", "/etc/", "//etc", "/etc//", "/./etc", "/etc/.", "/tmp/../etc", "/etc/ssl/..",
		"/etc/passwd", "/etc//passwd", "/usr/../etc/passwd",
		"/var/run/docker.sock", "/var//run/docker.sock", "/run/docker.sock", "/var/run/./docker.sock",
		"$DIR/etc-link", "$DIR/etc-link/", "$DIR/etc-link/passwd",
		"$DIR/root-link", "$DIR/root-link/etc", "$DIR/chain-link/etc",
		"$DIR/work/../root-link", "$DIR/missing/../../../etc",
	} {
		src = os.Expand(src, func(string) string { return s.dir })
		s.Run(src, func() {
			bind, mount := s.verdicts(src)
			s.Require().True(bind, "bind %q", src)
			s.Require().True(mount, "mount %q", src)
		})
	}
}

// The refusals above aren't because every path is refused: a plain
// workspace path, through a link or not, is let through.
func (s *BindAliasSpecSuite) TestWorkspaceSourceIsAllowed() {
	s.Require().NoError(os.Symlink("work", filepath.Join(s.dir, "work-link")))
	for _, src := range []string{s.dir + "/work", s.dir + "//work/", s.dir + "/work-link", "named-volume"} {
		s.Run(src, func() {
			bind, mount := s.verdicts(src)
			require.False(s.T(), bind, "bind %q", src)
			require.False(s.T(), mount, "mount %q", src)
		})
	}
}
