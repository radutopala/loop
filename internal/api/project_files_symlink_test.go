package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/chatcomponents"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/osutil"
)

// A project's .loop dir is the agent's to write. A shortcut or component
// file there that is a symlink out of the project is not served, whichever
// way the link is spelled, while one that stays in the project still is.
func (s *ServerSuite) TestProjectFilesDontFollowSymlinksOut() {
	s.registerComponentRoutes()
	host := s.T().TempDir()
	secret := filepath.Join(host, "id_ed25519")
	require.NoError(s.T(), os.WriteFile(secret, []byte("HOST SECRET"), 0o600))
	require.NoError(s.T(), os.Mkdir(filepath.Join(host, "keys"), 0o700))
	require.NoError(s.T(), os.WriteFile(filepath.Join(host, "keys", "guide.md"), []byte("HOST SECRET"), 0o600))

	project := s.T().TempDir()
	loopDir := filepath.Join(project, ".loop")
	for _, d := range []string{"shortcuts", "components/ok", "components/abs"} {
		require.NoError(s.T(), os.MkdirAll(filepath.Join(loopDir, d), 0o755))
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(project, "NOTES.md"), []byte("project notes"), 0o644))
	links := map[string]string{
		"shortcuts/abs.md":        secret,
		"shortcuts/rel.md":        "../../../" + filepath.Base(host) + "/id_ed25519",
		"shortcuts/inside.md":     "../../NOTES.md",
		"components/abs/guide.md": secret,
		"components/dir":          filepath.Join(host, "keys"),
		"components/ok/guide.md":  "../../../NOTES.md",
	}
	for link, target := range links {
		require.NoError(s.T(), os.Symlink(target, filepath.Join(loopDir, link)))
	}

	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{LoopDir: filepath.Join(host, ".loop")}, nil
	}
	s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: project}, nil)
	s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
		merged := *base
		merged.PromptShortcuts = []config.PromptShortcut{
			{Name: "abs", PromptPath: "abs.md"},
			{Name: "rel", PromptPath: "rel.md"},
			{Name: "inside", PromptPath: "inside.md"},
		}
		merged.ChatComponents = []config.ChatComponent{{Name: "abs"}, {Name: "dir"}, {Name: "ok"}}
		return &merged, nil
	}

	rec := s.testRequest("GET", "/api/shortcuts?channel_id=ch-proj", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var shortcuts []shortcutResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &shortcuts))
	require.Len(s.T(), shortcuts, 1)
	require.Equal(s.T(), "inside", shortcuts[0].Name)
	require.Equal(s.T(), "project notes", shortcuts[0].Prompt)

	rec = s.testRequest("GET", "/api/components?channel_id=ch-proj", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.NotContains(s.T(), rec.Body.String(), "HOST SECRET")
	var components []chatcomponents.Template
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &components))
	guides := map[string]string{}
	for _, c := range components {
		guides[c.Name] = c.Guide
	}
	require.Equal(s.T(), map[string]string{"abs": "", "dir": "", "ok": "project notes"},
		map[string]string{"abs": guides["abs"], "dir": guides["dir"], "ok": guides["ok"]})
}

// A project config write that a symlink under the project leads outside it
// — config.json to the global config, or .loop to another dir — is refused
// before anything is written, whether the route is the agent's or the
// owner's.
func (s *ServerSuite) TestProjectConfigWritesDontFollowSymlinksOut() {
	host := s.T().TempDir()
	global := filepath.Join(host, "config.json")
	require.NoError(s.T(), os.WriteFile(global, []byte("{}"), 0o644))

	writes := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"prompt shortcut", "POST", "/api/shortcuts", `{"action":"add","scope":"project","channel_id":"ch-proj","name":"x","prompt":"p"}`},
		{"bash shortcut", "POST", "/api/bash-shortcuts", `{"action":"add","scope":"project","channel_id":"ch-proj","name":"x","command":"id"}`},
		{"workflow", "POST", "/api/workflows", `{"action":"add","scope":"project","channel_id":"ch-proj","workflow":{"name":"w","nodes":[{"id":"b","type":"bash","script":"id"}]}}`},
		{"project config", "PUT", "/api/config/project?channel_id=ch-proj", `{"content":"{}"}`},
	}
	links := []struct {
		name string
		make func(project string)
	}{
		{"config.json to the global config", func(project string) {
			require.NoError(s.T(), os.Mkdir(filepath.Join(project, ".loop"), 0o755))
			require.NoError(s.T(), os.Symlink(global, filepath.Join(project, ".loop", "config.json")))
		}},
		{"dangling config.json out", func(project string) {
			require.NoError(s.T(), os.Mkdir(filepath.Join(project, ".loop"), 0o755))
			require.NoError(s.T(), os.Symlink(filepath.Join(host, "new.json"), filepath.Join(project, ".loop", "config.json")))
		}},
		{".loop to another dir", func(project string) {
			require.NoError(s.T(), os.Symlink(host, filepath.Join(project, ".loop")))
		}},
	}
	for _, l := range links {
		project := s.T().TempDir()
		l.make(project)
		s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: project}, nil)
		for _, w := range writes {
			s.Run(l.name+"/"+w.name, func() {
				rec := s.testRequest(w.method, w.target, w.body)
				require.Equal(s.T(), http.StatusBadRequest, rec.Code, rec.Body.String())
				require.Contains(s.T(), rec.Body.String(), "outside the project")
				s.sys.AssertNotCalled(s.T(), "WriteFile", filepath.Join(project, ".loop", "config.json"), mock.Anything, mock.Anything)
			})
		}
		s.store.ExpectedCalls = nil
	}

	s.Run("learn proposal", func() {
		project := s.T().TempDir()
		links[0].make(project)
		ch := &db.Channel{ChannelID: "ch-proj", DirPath: project}
		s.store.On("GetChannel", mock.Anything, "ch-proj").Return(ch, nil)
		_, _, _, err := s.srv.editLearnConfig(context.Background(), ch, &learn.PromptShortcut{Name: "x", Prompt: "p"}, true)
		require.ErrorContains(s.T(), err, "outside the project")
	})

	// A symlink that stays in the project is written through as before.
	s.Run("symlink inside the project", func() {
		project := s.T().TempDir()
		require.NoError(s.T(), os.Mkdir(filepath.Join(project, ".loop"), 0o755))
		require.NoError(s.T(), os.WriteFile(filepath.Join(project, "loop.json"), []byte("{}"), 0o644))
		require.NoError(s.T(), os.Symlink("../loop.json", filepath.Join(project, ".loop", "config.json")))
		s.store.ExpectedCalls = nil
		s.store.On("GetChannel", mock.Anything, "ch-proj").Return(&db.Channel{ChannelID: "ch-proj", DirPath: project}, nil)
		rec := s.testRequest("PUT", "/api/config/project?channel_id=ch-proj", `{"content":"{}"}`)
		require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	})
	content, err := os.ReadFile(global)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "{}", string(content))
}

// inProjectSystem is the real filesystem, but fails the test when a file
// under project is reached any way other than through root: the check
// before a write can't stop a symlink swapped in after it, only a
// confined write can.
type inProjectSystem struct {
	osutil.RealSystem
	t       require.TestingT
	project string
}

func (f inProjectSystem) unconfined(name string) {
	rel, err := filepath.Rel(f.project, name)
	require.False(f.t, err == nil && !strings.HasPrefix(rel, ".."), "%s reached without confinement", name)
}

func (f inProjectSystem) ReadFile(name string) ([]byte, error) {
	f.unconfined(name)
	return f.RealSystem.ReadFile(name)
}

func (f inProjectSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	f.unconfined(name)
	return f.RealSystem.WriteFile(name, data, perm)
}

func (f inProjectSystem) MkdirAll(name string, perm os.FileMode) error {
	f.unconfined(name)
	return f.RealSystem.MkdirAll(name, perm)
}

func (f inProjectSystem) Rename(oldpath, newpath string) error {
	f.unconfined(oldpath)
	f.unconfined(newpath)
	return f.RealSystem.Rename(oldpath, newpath)
}

func (f inProjectSystem) Remove(name string) error {
	f.unconfined(name)
	return f.RealSystem.Remove(name)
}

// Every read and write of a project config, by every route that makes
// one, goes through the project dir, so it can't be redirected by a
// symlink planted between a check and the write.
func (s *ServerSuite) TestProjectConfigIOStaysInTheProject() {
	dir := s.trustProject(`{"mounts": ["/a:/a"]}`)
	s.useConfigHistory()
	s.srv.sys = inProjectSystem{t: s.T(), project: dir}
	path := filepath.Join(dir, ".loop", "config.json")

	for _, w := range []struct{ method, target, body string }{
		{"PUT", "/api/config/project?channel_id=ch-1", `{"content":"{}"}`},
		{"POST", "/api/shortcuts", `{"action":"add","scope":"project","channel_id":"ch-1","name":"x","prompt":"p"}`},
		{"POST", "/api/bash-shortcuts", `{"action":"add","scope":"project","channel_id":"ch-1","name":"x","command":"id"}`},
		{"POST", "/api/workflows", `{"action":"add","scope":"project","channel_id":"ch-1","workflow":{"name":"w","nodes":[{"id":"b","type":"bash","script":"id"}]}}`},
	} {
		rec := s.testRequest(w.method, w.target, w.body)
		require.Equal(s.T(), http.StatusNoContent, rec.Code, w.target+": "+rec.Body.String())
	}
	ch := &db.Channel{ChannelID: "ch-1", DirPath: dir}
	_, _, _, err := s.srv.editLearnConfig(context.Background(), ch, &learn.PromptShortcut{Name: "y", Prompt: "p"}, true)
	require.NoError(s.T(), err)
	_, err = s.srv.pluginProject(context.Background(), ch)
	require.NoError(s.T(), err)

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var got configResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
	for _, want := range []string{`"x"`, `"y"`, `"w"`} {
		require.Contains(s.T(), got.Raw, want)
	}

	h := s.configHistory("/api/config/project/history?channel_id=ch-1")
	require.Equal(s.T(), path, h.Path)
	initial := h.Revisions[len(h.Revisions)-1].ID
	rec = s.testRequest("POST", "/api/config/history/"+strconv.FormatInt(initial, 10)+"/restore", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code, rec.Body.String())
	data, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"mounts": ["/a:/a"]}`, string(data))
	require.True(s.T(), s.projectTrustStatus().Trusted)
}

// A project config that a symlink leads outside the project isn't read
// for the owner either: the settings, history and trust views show nothing
// of the file it points to.
func (s *ServerSuite) TestProjectConfigReadsDontFollowSymlinksOut() {
	dir := s.trustProject("")
	s.useConfigHistory()
	host := filepath.Join(s.T().TempDir(), "host.json")
	require.NoError(s.T(), os.WriteFile(host, []byte(`{"mounts": ["/HOST:/HOST"]}`), 0o644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, ".loop"), 0o755))
	require.NoError(s.T(), os.Symlink(host, filepath.Join(dir, ".loop", "config.json")))

	rec := s.testRequest("GET", "/api/config/project?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "project config leads outside the project")
	require.NotContains(s.T(), rec.Body.String(), "HOST")

	rec = s.testRequest("GET", "/api/config/project/trust?channel_id=ch-1", "")
	require.NotContains(s.T(), rec.Body.String(), "HOST")

	s.srv.recordConfig(filepath.Join(dir, ".loop", "config.json"), db.ConfigSourceExternal)
	require.Empty(s.T(), s.configHistory("/api/config/project/history?channel_id=ch-1").Revisions)

	_, err := s.srv.pluginProject(context.Background(), &db.Channel{ChannelID: "ch-1", DirPath: dir})
	require.ErrorContains(s.T(), err, "reading the project config")
}
