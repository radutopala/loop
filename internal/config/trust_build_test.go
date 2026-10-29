package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/buildctx"
)

const dockerfilePath = "/proj/.loop/container/Dockerfile"

func (s *TrustSuite) trusted() bool {
	st, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	return st.Trusted
}

// TestBuildContextNeedsTrust: a change to .loop/container waits for the
// owner like a config change, and shows in the diff.
func (s *TrustSuite) TestBuildContextNeedsTrust() {
	s.project(`{"container_image": "loop-agent:proj"}`)
	require.NoError(s.T(), s.store.Trust("/proj", ""))
	require.True(s.T(), s.trusted())

	s.files[dockerfilePath] = []byte("FROM loop-agent\nRUN rm /usr/local/bin/loop\n")
	st, err := s.store.Status("/proj")
	require.NoError(s.T(), err)
	require.False(s.T(), st.Trusted)
	require.Contains(s.T(), st.Diff, `+  "container_build": {`)
	require.Contains(s.T(), st.Diff, `+    "Dockerfile": "sha256:`)

	got, err := s.store.TrustedBuild("/proj")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got, "not trusted yet")

	require.NoError(s.T(), s.store.Trust("/proj", st.Hash))
	require.True(s.T(), s.trusted())
	got, err = s.store.TrustedBuild("/proj")
	require.NoError(s.T(), err)
	require.Equal(s.T(), buildctx.Files{"Dockerfile": buildctx.FileDigest(s.files[dockerfilePath], 0o644)}, got)
}

// TestBuildContextLeavesConfigFields: the builder checks the context, so a
// changed one doesn't take the project's trusted config fields away.
func (s *TrustSuite) TestBuildContextLeavesConfigFields() {
	main := &Config{}
	s.project(`{"mounts": ["/a:/a"]}`)
	s.files[dockerfilePath] = []byte("FROM loop-agent\n")
	require.NoError(s.T(), s.store.Trust("/proj", ""))

	s.files[dockerfilePath] = []byte("FROM loop-agent\nRUN true\n")
	require.False(s.T(), s.trusted())
	merged, err := s.loader().loadProjectConfig("/proj", main)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/a:/a"}, merged.Mounts)
}

// TestKeepLeavesBuildContext: an owner's config edit keeps the project
// trusted only as far as the build context is the trusted one.
func (s *TrustSuite) TestKeepLeavesBuildContext() {
	before := []byte(`{"mounts": ["/a:/a"]}`)
	after := []byte(`{"mounts": ["/a:/a", "/b:/b"]}`)
	s.project(string(before))
	s.files[dockerfilePath] = []byte("FROM loop-agent\n")
	require.NoError(s.T(), s.store.Trust("/proj", ""))

	s.project(string(after))
	require.NoError(s.T(), s.store.Keep("/proj", before, after))
	require.True(s.T(), s.trusted())

	// An agent's Dockerfile change isn't approved by the owner's next edit.
	s.files[dockerfilePath] = []byte("FROM loop-agent\nRUN true\n")
	s.project(string(before))
	require.NoError(s.T(), s.store.Keep("/proj", after, before))
	require.False(s.T(), s.trusted())
}

func (s *TrustSuite) TestKeepBuild() {
	written := []byte("FROM loop-agent:latest\n")
	tests := []struct {
		name     string
		trusted  string // trusted config; "" for never trusted
		trustCtx map[string]string
		config   string
		ctx      map[string]string // the build context on disk, Loop's file included
		kept     bool
	}{
		{name: "never trusted, just the file", ctx: map[string]string{"Dockerfile": string(written)}, kept: true},
		{name: "trusted, just the file", trusted: `{"container_image": "x"}`, config: `{"container_image": "x"}`, ctx: map[string]string{"Dockerfile": string(written)}, kept: true},
		{name: "trusted context plus the file", trusted: `{"container_image": "x"}`, trustCtx: map[string]string{"ca.pem": "ca"}, config: `{"container_image": "x"}`, ctx: map[string]string{"Dockerfile": string(written), "ca.pem": "ca"}, kept: true},
		{name: "another file too", ctx: map[string]string{"Dockerfile": string(written), "evil.sh": "x"}},
		{name: "the file differs", ctx: map[string]string{"Dockerfile": "FROM loop-agent\nRUN x\n"}},
		{name: "config changed", trusted: `{"container_image": "x"}`, config: `{"container_image": "y"}`, ctx: map[string]string{"Dockerfile": string(written)}},
		{name: "never trusted, config needs trust", config: `{"mounts": ["/:/host"]}`, ctx: map[string]string{"Dockerfile": string(written)}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if tt.trusted != "" {
				s.project(tt.trusted)
				for name, data := range tt.trustCtx {
					s.files[filepath.Join("/proj/.loop/container", name)] = []byte(data)
				}
				require.NoError(s.T(), s.store.Trust("/proj", ""))
			}
			if tt.config != "" {
				s.project(tt.config)
			}
			for name, data := range tt.ctx {
				s.files[filepath.Join("/proj/.loop/container", name)] = []byte(data)
			}
			before := s.files[trustPath]
			require.NoError(s.T(), s.store.KeepBuild("/proj", "Dockerfile", written, 0o644))
			if !tt.kept {
				require.Equal(s.T(), before, s.files[trustPath])
				return
			}
			require.True(s.T(), s.trusted())
		})
	}
}

func (s *TrustSuite) TestBuildContextErrors() {
	boom := errors.New("boom")
	s.project(`{"mounts": ["/a:/a"]}`)
	require.NoError(s.T(), s.store.Trust("/proj", ""))
	s.store.buildFiles = func(string) (buildctx.Files, error) { return nil, boom }

	_, err := s.store.Status("/proj")
	require.ErrorIs(s.T(), err, boom)
	require.ErrorContains(s.T(), err, "reading the project's .loop/container")
	require.ErrorIs(s.T(), s.store.Keep("/proj", []byte(`{}`), []byte(`{}`)), boom)
	require.ErrorIs(s.T(), s.store.KeepBuild("/proj", "Dockerfile", nil, 0o644), boom)

	s.store.buildFiles = func(string) (buildctx.Files, error) { return nil, nil }
	s.files[trustPath] = []byte("not json")
	require.ErrorContains(s.T(), s.store.KeepBuild("/proj", "Dockerfile", nil, 0o644), "parsing")
	_, err = s.store.TrustedBuild("/proj")
	require.ErrorContains(s.T(), err, "parsing")
}

// TestProjectBuildFiles runs on a real dir: the context is read without
// following a link out of the project.
func (s *TrustSuite) TestProjectBuildFiles() {
	dir := s.T().TempDir()
	got, err := projectBuildFiles(&buildctx.Cache{}, dir)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got, "no .loop/container")

	ctx := filepath.Join(dir, ".loop", "container")
	require.NoError(s.T(), os.MkdirAll(ctx, 0o755))
	got, err = projectBuildFiles(&buildctx.Cache{}, dir)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got, "an empty context is none")

	require.NoError(s.T(), os.WriteFile(filepath.Join(ctx, "Dockerfile"), []byte("FROM x\n"), 0o644))
	got, err = projectBuildFiles(&buildctx.Cache{}, dir)
	require.NoError(s.T(), err)
	require.Equal(s.T(), buildctx.Files{"Dockerfile": buildctx.FileDigest([]byte("FROM x\n"), 0o644)}, got)

	require.NoError(s.T(), os.RemoveAll(ctx))
	require.NoError(s.T(), os.Symlink(s.T().TempDir(), ctx))
	_, err = projectBuildFiles(&buildctx.Cache{}, dir)
	require.Error(s.T(), err, "a link out of the project")
}
