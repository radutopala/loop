package container

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/dockerproxy"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

type ProtectedSuite struct {
	suite.Suite

	sys    *testutil.MockSystem
	runner *DockerRunner
}

func TestProtectedSuite(t *testing.T) {
	suite.Run(t, new(ProtectedSuite))
}

// SetupTest protects /cfg/loop (reached through the /link/loop symlink too)
// and /home/u/.loop/run.
func (s *ProtectedSuite) SetupTest() {
	s.sys = new(testutil.MockSystem)
	s.sys.On("EvalSymlinks", "/cfg/loop").Return("/private/cfg/loop", nil)
	s.sys.On("EvalSymlinks", "/home/u/.loop/run").Return("/home/u/.loop/run", nil)
	s.sys.On("EvalSymlinks", "/link/loop").Return("/private/cfg/loop", nil)
	s.sys.On("EvalSymlinks", "/link/home").Return("/home/u", nil)
	s.sys.On("EvalSymlinks", mock.Anything).Return("", os.ErrNotExist)
	s.runner = &DockerRunner{sys: s.sys}
	s.runner.SetProtectedDirs([]string{"/cfg/loop", "/home/u/.loop/run"})
}

func (s *ProtectedSuite) TestDefaultProtectedDirs() {
	ucd := func(dir string, err error) func() (string, error) {
		return func() (string, error) { return dir, err }
	}
	tests := []struct {
		name      string
		policyDir string
		ucd       func() (string, error)
		want      []string
	}{
		{"both", "/home/u/.loop/run/", ucd("/home/u/.config", nil), []string{"/home/u/.config/loop", "/home/u/.loop/run"}},
		{"no config dir", "/home/u/.loop/run", ucd("", errors.New("no $HOME")), []string{"/home/u/.loop/run"}},
		{"empty config dir", "/home/u/.loop/run", ucd("", nil), []string{"/home/u/.loop/run"}},
		{"no policy dir", "", ucd("/cfg", nil), []string{"/cfg/loop"}},
		{"neither", "", ucd("", errors.New("no $HOME")), nil},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, DefaultProtectedDirs(tc.policyDir, tc.ucd))
		})
	}
}

func (s *ProtectedSuite) TestSetProtectedDirs() {
	require.Equal(s.T(), []string{"/cfg/loop", "/private/cfg/loop", "/home/u/.loop/run"}, s.runner.protectedDirs)

	s.runner.SetProtectedDirs([]string{"/gone"})
	require.Equal(s.T(), []string{"/gone"}, s.runner.protectedDirs, "replaces the previous set; unresolvable dirs are kept as given")
}

func (s *ProtectedSuite) TestPathsOverlap() {
	tests := []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a", "/a/b", true},
		{"/", "/a/b", true},
		{"/A/B", "/a/b", true},
		{"/a/bc", "/a/b", false},
		{"/a/c", "/a/b", false},
	}
	for _, tc := range tests {
		s.Run(tc.a+" "+tc.b, func() {
			require.Equal(s.T(), tc.want, pathsOverlap(tc.a, tc.b))
		})
	}
}

func (s *ProtectedSuite) TestDropProtectedBinds() {
	s.sys.On("Stat", "/home/u/.loop/run").Return(nil, nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	in := []string{
		"/work/app:/work/app",
		"/cfg/loop:/x:ro",              // the dir itself, even read-only
		"/cfg/loop/owner-token:/t",     // under it
		"/home/u:/home/u",              // a writable ancestor
		"/home/u/.loop:/l:ro",          // a read-only ancestor: masked
		"/Home/U/.Loop/:/c:ro,z",       // case folded, with more mode flags
		"/cfg:/cfg:ro",                 // a read-only ancestor of a missing dir
		"/:/host:ro",                   // the root: reaches both dirs
		"/CFG/Loop:/x",                 // case folded
		"/link/loop:/x",                // through a symlink
		"/link/home:/h",                // symlink to a writable ancestor
		"/private/cfg/loop/a:/x",       // resolved spelling
		"/home/u/.loop/runner:/x",      // sibling prefix
		"cache:/cache",                 // named volume
		"bogus",                        // unparseable, kept for later checks
		"/home/u/.loop/run/ch-1:/p:ro", // policy dir child
	}
	kept, masks := s.runner.dropProtectedBinds(in)
	require.Equal(s.T(), []string{
		"/work/app:/work/app",
		"/home/u/.loop:/l:ro",
		"/Home/U/.Loop/:/c:ro,z",
		"/home/u/.loop/runner:/x",
		"cache:/cache",
		"bogus",
	}, kept)
	require.Equal(s.T(), []string{"/l/run", "/c/run"}, masks)
}

// A read-only symlink to an ancestor is masked below its container path,
// however the protected dir is reached.
func (s *ProtectedSuite) TestDropProtectedBindsMasksThroughSymlink() {
	s.sys.On("Stat", mock.Anything).Return(nil, nil)
	kept, masks := s.runner.dropProtectedBinds([]string{"/link/home:/h:ro"})
	require.Equal(s.T(), []string{"/link/home:/h:ro"}, kept)
	require.Equal(s.T(), []string{"/h/.loop/run"}, masks)
}

func (s *ProtectedSuite) TestRelUnder() {
	tests := []struct {
		ancestor, dir, rel string
		under              bool
	}{
		{"/a", "/a/b/c", "b/c", true},
		{"/a/", "/a/b", "b", true},
		{"/", "/a/b", "a/b", true},
		{"/A", "/a/B", "B", true},
		{"/a", "/a", "", false},
		{"/a/b", "/a", "", false},
		{"/a", "/ab", "", false},
	}
	for _, tc := range tests {
		s.Run(tc.ancestor+" "+tc.dir, func() {
			rel, under := relUnder(tc.ancestor, tc.dir)
			require.Equal(s.T(), tc.rel, rel)
			require.Equal(s.T(), tc.under, under)
		})
	}
}

func (s *ProtectedSuite) TestDropProtectedBindsNoDirs() {
	r := &DockerRunner{sys: s.sys}
	in := []string{"/cfg/loop:/x"}
	kept, masks := r.dropProtectedBinds(in)
	require.Equal(s.T(), in, kept)
	require.Nil(s.T(), masks)
	require.Nil(s.T(), r.protectedDirBodyRules())
}

// The rules deny nested containers and volumes reaching a protected dir,
// however its path is spelled, and leave everything else to later rules.
func (s *ProtectedSuite) TestProtectedDirBodyRules() {
	policy, err := dockerproxy.CompilePolicy(types.DecisionAllow, nil, s.runner.protectedDirBodyRules())
	require.NoError(s.T(), err)
	policy.SetSymlinkResolver(func(p string) (string, error) {
		if p == "/work/link" {
			return "/cfg/loop/owner-token", nil
		}
		if p == "/work/missing" {
			return "", os.ErrNotExist
		}
		return p, nil
	})

	hc := func(v map[string]any) map[string]any { return map[string]any{"HostConfig": v} }
	binds := func(b ...string) map[string]any { return hc(map[string]any{"Binds": toAny(b)}) }
	mount := func(m map[string]any) map[string]any { return hc(map[string]any{"Mounts": []any{m}}) }
	create := []struct {
		name string
		body map[string]any
		want types.Decision
	}{
		{"bind", binds("/cfg/loop:/x"), types.DecisionDeny},
		{"bind under", binds("/work:/w", "/cfg/loop/owner-token:/t:ro"), types.DecisionDeny},
		{"bind resolved spelling", binds("/private/cfg/loop:/x"), types.DecisionDeny},
		{"bind case", binds("/Home/U/.loop/Run:/x"), types.DecisionDeny},
		{"desktop host_mnt", binds("/host_mnt/cfg/loop:/x"), types.DecisionDeny},
		{"desktop mnt host", binds("/run/desktop/mnt/host/cfg/loop:/x"), types.DecisionDeny},
		{"bind symlink", binds("/work/link:/x"), types.DecisionDeny},
		{"bind dotdot", binds("/work/../cfg/loop:/x"), types.DecisionDeny},
		{"mount source", mount(map[string]any{"Type": "bind", "Source": "/cfg/loop", "Target": "/x"}), types.DecisionDeny},
		{"volume device", mount(map[string]any{"Type": "volume", "Target": "/x", "VolumeOptions": map[string]any{
			"DriverConfig": map[string]any{"Options": map[string]any{"type": "none", "o": "bind", "device": "/home/u/.loop/run"}},
		}}), types.DecisionDeny},
		{"sibling", binds("/cfg/loopy:/x"), ""},
		{"ancestor", binds("/cfg:/x:ro"), types.DecisionDeny},
		{"ancestor trailing slash", binds("/home/u/.loop/:/x:ro"), types.DecisionDeny},
		{"ancestor mount", mount(map[string]any{"Type": "bind", "Source": "/home/u", "Target": "/x", "ReadOnly": true}), types.DecisionDeny},
		{"desktop root", binds("/host_mnt:/x:ro"), types.DecisionDeny},
		{"sibling of ancestor", binds("/home/u/.loopy:/x"), ""},
		{"child of ancestor", binds("/home/u/.loop/config.json:/x:ro"), ""},
		{"unresolvable", binds("/work/missing:/x"), ""},
		{"named volume", binds("loop:/x"), ""},
	}
	for _, tc := range create {
		s.Run(tc.name, func() {
			got := policy.CheckBody("POST", "/containers/create", "application/json", tc.body)
			require.Equal(s.T(), tc.want, got.Decision, "%+v", got)
		})
	}

	got := policy.CheckBody("POST", "/volumes/create", "application/json",
		map[string]any{"Name": "v", "DriverOpts": map[string]any{"type": "none", "o": "bind", "device": "/cfg/loop"}})
	require.Equal(s.T(), types.DecisionDeny, got.Decision)
	require.Equal(s.T(), "loop's credential and policy dirs can't be mounted into containers", got.Message)
	got = policy.CheckBody("POST", "/volumes/create", "application/json",
		map[string]any{"Name": "v", "DriverOpts": map[string]any{"type": "tmpfs", "device": "tmpfs"}})
	require.Empty(s.T(), got.Decision)
}

// The protected-dir rules go ahead of every configured rule, so no allow or
// agent-mount exemption can relax them.
func (s *ProtectedSuite) TestWriteProxyPolicyFilePrependsProtectedRules() {
	var captured []byte
	s.sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	s.sys.On("WriteFile", "/home/u/.loop/run/ch-1/proxy-policy.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { captured = append([]byte(nil), args.Get(1).([]byte)...) }).
		Return(nil)
	s.runner.policyDir = "/home/u/.loop/run"
	cfg := &config.Config{Gates: config.GatesConfig{DockerProxy: config.DockerProxyConfig{
		Enabled:   true,
		BodyRules: config.DefaultDockerProxyBodyRules(),
	}}}

	_, err := s.runner.writeProxyPolicyFile(cfg, "ch-1", []string{"/home/u:/home/u"})
	require.NoError(s.T(), err)

	var got proxyPolicyJSON
	require.NoError(s.T(), json.Unmarshal(captured, &got))
	protected := s.runner.protectedDirBodyRules()
	require.Greater(s.T(), len(got.BodyRules), len(protected))
	require.Equal(s.T(), protected, got.BodyRules[:len(protected)])
}

func (s *RunnerSuite) TestBuildContainerMountsDropsProtectedDirs() {
	sys := new(testutil.MockSystem)
	sys.On("EvalSymlinks", mock.Anything).Return("", os.ErrNotExist)
	sys.On("UserHomeDir").Return("/home/u", nil)
	sys.On("Stat", "/home/u/.loop").Return(nil, nil)
	sys.On("Stat", "/home/u/.loop/run").Return(nil, nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	sys.On("ExecCommandOutput", mock.Anything, mock.Anything).Return([]byte{}, nil)
	s.runner.sys = sys
	s.runner.SetProtectedDirs([]string{"/home/u/.loop/run"})

	binds, masks, _ := s.runner.buildContainerMounts([]string{"~/.loop:~/.loop:ro"}, "/home/u", "", []string{"/home/u/.loop/run/ch-1", "/work/lib"})
	require.Equal(s.T(), []string{"/home/u/.loop:/home/u/.loop:ro", "/work/lib:/work/lib"}, binds)
	require.Equal(s.T(), []string{"/home/u/.loop/run"}, masks)
}
