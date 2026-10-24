package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// The agent can write every playground (and the project's .loop), so it can
// plant symlinks there. These specs plant real ones, absolute, relative,
// dangling, to a dir, and through the playground base itself, then drive
// every playground and paste operation at them: none may serve, overwrite,
// create or delete anything outside, which holds a marker in its file
// contents and names.

const hostMarker = "HOSTSECRET"

// playgroundSpec is the tree the specs work in.
type playgroundSpec struct {
	outside string // the host's files, which nothing may reach
	project string // ch1's project dir
	linked  string // ch2's project dir, whose .loop/playground links outside
	inProj  string // ch3's project dir, whose .loop/playground links in it
}

// setupPlaygroundSpec builds the tree: in both the global and ch1's project
// playground base, a playground demo with plain files, an in-playground link
// and links out of every kind; per kind, a playground whose index.html,
// README.md, state.json and importmap.json all link out that way; and
// playgrounds that are themselves links out of the base.
func (s *ServerSuite) setupPlaygroundSpec(chans map[string]*db.Channel) playgroundSpec {
	loopDir := s.setPlaygroundDir()
	sp := playgroundSpec{
		outside: s.realTempDir(),
		project: s.realTempDir(),
		linked:  s.realTempDir(),
		inProj:  s.realTempDir(),
	}
	write := func(p, data string) {
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(s.T(), os.WriteFile(p, []byte(data), 0o644))
	}
	link := func(target, p string) {
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(s.T(), os.Symlink(target, p))
	}
	rel := func(from, to string) string {
		r, err := filepath.Rel(filepath.Dir(from), to)
		require.NoError(s.T(), err)
		return r
	}

	named := []string{"index.html", "README.md", "state.json", "importmap.json"}
	write(filepath.Join(sp.outside, hostMarker), hostMarker)
	for _, n := range named {
		write(filepath.Join(sp.outside, n), "---\ntitle: "+hostMarker+"\n---\n"+hostMarker)
		write(filepath.Join(sp.outside, "demo", n), hostMarker)
	}

	for _, base := range []string{filepath.Join(loopDir, "playground"), filepath.Join(sp.project, ".loop", "playground")} {
		demo := filepath.Join(base, "demo")
		write(filepath.Join(demo, "index.html"), "<p>in-root</p>")
		write(filepath.Join(demo, "plain.txt"), "plain")
		link("plain.txt", filepath.Join(demo, "inside.txt"))
		link(filepath.Join(sp.outside, hostMarker), filepath.Join(demo, "abs"))
		link(rel(filepath.Join(demo, "rel"), filepath.Join(sp.outside, hostMarker)), filepath.Join(demo, "rel"))
		link(filepath.Join(sp.outside, "new"), filepath.Join(demo, "dangling"))
		link(sp.outside, filepath.Join(demo, "out"))
		write(filepath.Join(base, "sibling", "x.txt"), "sibling")
		link("../sibling/x.txt", filepath.Join(demo, "sib"))
		link("demo", filepath.Join(base, "alias"))

		for _, n := range named {
			p := filepath.Join(base, "fabs", n)
			link(filepath.Join(sp.outside, n), p)
			p = filepath.Join(base, "frel", n)
			link(rel(p, filepath.Join(sp.outside, n)), p)
			link(filepath.Join(sp.outside, "new-"+n), filepath.Join(base, "fdangling", n))
		}
		link(filepath.Join(sp.outside, "demo"), filepath.Join(base, "pabs"))
		link(rel(filepath.Join(base, "prel"), filepath.Join(sp.outside, "demo")), filepath.Join(base, "prel"))
	}

	link(sp.outside, filepath.Join(sp.linked, ".loop", "playground"))
	write(filepath.Join(sp.inProj, "src", "demo", "index.html"), hostMarker)
	link("../src", filepath.Join(sp.inProj, ".loop", "playground"))

	chans["ch1"].DirPath, chans["ch2"].DirPath, chans["ch3"].DirPath = sp.project, sp.linked, sp.inProj
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{PlaygroundShare: config.PlaygroundShareConfig{Enabled: true}}, nil
	}
	s.srv.playground.setTunnel(&fakeTunnel{})
	return sp
}

// specChannels registers ch1, ch2 and ch3 once per test; each
// setupPlaygroundSpec points them at its project dirs.
func (s *ServerSuite) specChannels() map[string]*db.Channel {
	chans := map[string]*db.Channel{}
	for _, ch := range []string{"ch1", "ch2", "ch3"} {
		chans[ch] = &db.Channel{ChannelID: ch}
		s.store.On("GetChannel", mock.Anything, ch).Return(chans[ch], nil).Maybe()
	}
	return chans
}

// snapshot records every entry under dir: its kind and, for files, content.
func (s *ServerSuite) snapshot(dir string) map[string]string {
	got := map[string]string{}
	require.NoError(s.T(), filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		require.NoError(s.T(), err)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			got[p] = "link"
		case d.IsDir():
			got[p] = "dir"
		default:
			data, err := os.ReadFile(p)
			require.NoError(s.T(), err)
			got[p] = string(data)
		}
		return nil
	}))
	return got
}

// playgroundOp is one request against playground name and file path rel,
// in the scope of q ("" for global, else "&scope=project&channel_id=…").
type playgroundOp struct {
	name string
	req  func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder
}

func playgroundOps() []playgroundOp {
	do := func(method, url, body string) func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder {
		return func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder {
			u := strings.NewReplacer("{name}", name, "{rel}", rel, "{q}", q).Replace(url)
			return s.testRequest(method, u, body)
		}
	}
	// The serve handlers are called directly: the mux would clean a ".."
	// out of the URL before it reached them.
	serve := func(withFile bool) func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder {
		return func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder {
			req := httptest.NewRequest("GET", "/api/playground/serve", nil)
			req.SetPathValue("name", name)
			req.SetPathValue("channel_id", ch)
			req.SetPathValue("path", rel)
			rec := httptest.NewRecorder()
			pg := s.srv.playground
			switch {
			case ch == "" && withFile:
				pg.handlePlaygroundServeFile(rec, req)
			case ch == "":
				pg.handlePlaygroundServe(rec, req)
			case withFile:
				pg.handlePlaygroundServeProjectFile(rec, req)
			default:
				pg.handlePlaygroundServeProject(rec, req)
			}
			return rec
		}
	}
	shared := func(withFile bool) func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder {
		return func(s *ServerSuite, ch, q, name, rel string) *httptest.ResponseRecorder {
			var loc playgroundLoc
			var err error
			if ch == "" {
				loc, err = s.srv.playground.validatePlaygroundDir(name)
			} else {
				loc, err = s.srv.playground.projectPlayground(s.T().Context(), ch, name)
			}
			rec := httptest.NewRecorder()
			if err != nil {
				return rec // a name the share route refuses can't be shared
			}
			tok := s.srv.playground.shares.add(name, "x", ch, loc)
			req := httptest.NewRequest("GET", "/p/"+tok, nil)
			req.SetPathValue("token", tok)
			if withFile {
				req.SetPathValue("path", rel)
				s.srv.playground.handleSharedPlaygroundServeFile(rec, req)
			} else {
				s.srv.playground.handleSharedPlaygroundServe(rec, req)
			}
			return rec
		}
	}
	return []playgroundOp{
		{"get", do("GET", "/api/playground?name={name}{q}", "")},
		{"update", do("PUT", "/api/playground?name={name}{q}", `{"html":"pwned","title":"pwned"}`)},
		{"delete", do("DELETE", "/api/playground?name={name}{q}", "")},
		{"file read", do("GET", "/api/playground/file?name={name}&path={rel}{q}", "")},
		{"file write", do("PUT", "/api/playground/file?name={name}&path={rel}{q}", "pwned")},
		{"file write below", do("PUT", "/api/playground/file?name={name}&path={rel}/new{q}", "pwned")},
		{"file delete", do("DELETE", "/api/playground/file?name={name}&path={rel}{q}", "")},
		{"file list", do("GET", "/api/playground/files?name={name}{q}", "")},
		{"state get", do("GET", "/api/playground/state?name={name}{q}", "")},
		{"state patch", do("PATCH", "/api/playground/state?name={name}{q}", `{"a":"pwned"}`)},
		{"share", do("PUT", "/api/playground/share?name={name}{q}", "")},
		{"serve", serve(false)},
		{"serve file", serve(true)},
		{"shared serve", shared(false)},
		{"shared serve file", shared(true)},
	}
}

func (s *ServerSuite) TestPlaygroundSymlinkSpec() {
	chans := s.specChannels()
	type target struct{ name, rel string }
	escapes := map[string]target{
		"absolute file link":            {"demo", "abs"},
		"relative file link":            {"demo", "rel"},
		"dangling file link":            {"demo", "dangling"},
		"symlinked dir":                 {"demo", "out/" + hostMarker},
		"dot-dot":                       {"demo", "../../" + hostMarker},
		"link to a sibling playground":  {"demo", "sib"},
		"absolute named-file links":     {"fabs", "index.html"},
		"relative named-file links":     {"frel", "index.html"},
		"dangling named-file links":     {"fdangling", "index.html"},
		"absolute playground link":      {"pabs", "index.html"},
		"relative playground link":      {"prel", "index.html"},
		"dot-dot playground":            {"..", "index.html"},
		"playground climbing by name":   {"demo/../../..", "index.html"},
		"base linked outside (project)": {"demo", "index.html"},
		"base linked within (project)":  {"demo", "index.html"},
	}
	for _, scope := range []string{"global", "project"} {
		for kind, tg := range escapes {
			ch := "ch1"
			switch kind {
			case "base linked outside (project)":
				ch = "ch2"
			case "base linked within (project)":
				ch = "ch3"
			}
			if scope == "global" && ch != "ch1" {
				continue
			}
			for _, op := range playgroundOps() {
				s.Run(scope+"/"+kind+"/"+op.name, func() {
					sp := s.setupPlaygroundSpec(chans)
					before := s.snapshot(sp.outside)
					inProjBefore := s.snapshot(filepath.Join(sp.inProj, "src"))
					q, c := "", ""
					if scope == "project" {
						q, c = "&scope=project&channel_id="+ch, ch
					}
					rec := op.req(s, c, q, tg.name, tg.rel)
					require.NotContains(s.T(), rec.Body.String(), hostMarker, "served a host file")
					require.NotContains(s.T(), rec.Body.String(), "sibling", "served a sibling playground's file")
					require.Equal(s.T(), before, s.snapshot(sp.outside), "touched a host file")
					require.Equal(s.T(), inProjBefore, s.snapshot(filepath.Join(sp.inProj, "src")), "touched the project outside its playgrounds")
					s.srv.playground.shares = newShareStore()
				})
			}
		}
	}
}

func (s *ServerSuite) TestPlaygroundSymlinkSpecList() {
	chans := s.specChannels()
	sp := s.setupPlaygroundSpec(chans)
	before := s.snapshot(sp.outside)
	for _, ch := range []string{"ch1", "ch2", "ch3"} {
		rec := s.testRequest("GET", "/api/playground/items?channel_id="+ch, "")
		require.Equal(s.T(), http.StatusOK, rec.Code)
		require.NotContains(s.T(), rec.Body.String(), hostMarker, ch)
	}
	require.Equal(s.T(), before, s.snapshot(sp.outside))
}

// Negative control: plain files and links that stay in the playground keep
// working, in both scopes.
func (s *ServerSuite) TestPlaygroundSymlinkSpecInRoot() {
	chans := s.specChannels()
	for _, q := range []string{"", "&scope=project&channel_id=ch1"} {
		s.Run("scope"+q, func() {
			s.setupPlaygroundSpec(chans)
			for _, p := range []string{"plain.txt", "inside.txt"} {
				rec := s.testRequest("GET", "/api/playground/file?name=demo&path="+p+q, "")
				require.Equal(s.T(), http.StatusOK, rec.Code, p)
				require.Equal(s.T(), "plain", rec.Body.String(), p)
			}
			rec := s.testRequest("GET", "/api/playground?name=alias"+q, "")
			require.Equal(s.T(), http.StatusOK, rec.Code, "a playground linked to another in the base")
			require.Contains(s.T(), rec.Body.String(), "in-root")

			rec = s.testRequest("PUT", "/api/playground/file?name=demo&path=inside.txt"+q, "changed")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			rec = s.testRequest("GET", "/api/playground/file?name=demo&path=plain.txt"+q, "")
			require.Equal(s.T(), "changed", rec.Body.String(), "wrote through the in-playground link")

			rec = s.testRequest("PUT", "/api/playground/file?name=demo&path=lib/new.js"+q, "new")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			rec = s.testRequest("PATCH", "/api/playground/state?name=demo"+q, `{"a":1}`)
			require.Equal(s.T(), http.StatusOK, rec.Code)
			rec = s.testRequest("DELETE", "/api/playground/file?name=demo&path=lib/new.js"+q, "")
			require.Equal(s.T(), http.StatusNoContent, rec.Code)
			rec = s.testRequest("DELETE", "/api/playground/file?name=demo&path=missing"+q, "")
			require.Equal(s.T(), http.StatusNotFound, rec.Code)
			rec = s.testRequest("PUT", "/api/playground/share?name=alias"+q, "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			rec = s.testRequest("PUT", "/api/playground/share?name=pabs"+q, "")
			require.Equal(s.T(), http.StatusBadRequest, rec.Code, "a playground linked out of the base isn't shared")
			rec = s.testRequest("DELETE", "/api/playground?name=sibling"+q, "")
			require.Equal(s.T(), http.StatusNoContent, rec.Code)
		})
	}
}

func (s *ServerSuite) TestPasteImageSymlinkSpec() {
	tests := []struct {
		name  string
		plant func(project, outside string)
	}{
		{"pastes dir linked outside", func(project, outside string) {
			require.NoError(s.T(), os.MkdirAll(filepath.Join(project, ".loop"), 0o755))
			require.NoError(s.T(), os.Symlink(outside, filepath.Join(project, ".loop", "pastes")))
		}},
		{".loop linked outside", func(project, outside string) {
			require.NoError(s.T(), os.Symlink(outside, filepath.Join(project, ".loop")))
		}},
		{"pastes dir linked by a relative path", func(project, outside string) {
			require.NoError(s.T(), os.MkdirAll(filepath.Join(project, ".loop"), 0o755))
			r, err := filepath.Rel(filepath.Join(project, ".loop"), outside)
			require.NoError(s.T(), err)
			require.NoError(s.T(), os.Symlink(r, filepath.Join(project, ".loop", "pastes")))
		}},
		{"pastes dir dangling outside", func(project, outside string) {
			require.NoError(s.T(), os.MkdirAll(filepath.Join(project, ".loop"), 0o755))
			require.NoError(s.T(), os.Symlink(filepath.Join(outside, "new"), filepath.Join(project, ".loop", "pastes")))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			project, outside := s.realTempDir(), s.realTempDir()
			tc.plant(project, outside)
			s.store.On("GetChannel", mock.Anything, "paste").Return(&db.Channel{ChannelID: "paste", DirPath: project}, nil).Once()

			rec := s.testRequest("POST", "/api/channels/paste/paste-image", pasteBody([]byte("PNG"), "image/png"))
			require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
			require.Equal(s.T(), map[string]string{outside: "dir"}, s.snapshot(outside), "wrote outside the project")
		})
	}

	s.Run("pastes dir linked within the project", func() {
		project := s.realTempDir()
		require.NoError(s.T(), os.MkdirAll(filepath.Join(project, "img"), 0o755))
		require.NoError(s.T(), os.MkdirAll(filepath.Join(project, ".loop"), 0o755))
		require.NoError(s.T(), os.Symlink("../img", filepath.Join(project, ".loop", "pastes")))
		s.store.On("GetChannel", mock.Anything, "paste").Return(&db.Channel{ChannelID: "paste", DirPath: project}, nil).Once()

		rec := s.testRequest("POST", "/api/channels/paste/paste-image", pasteBody([]byte("PNG"), "image/png"))
		require.Equal(s.T(), http.StatusOK, rec.Code)
		entries, err := os.ReadDir(filepath.Join(project, "img"))
		require.NoError(s.T(), err)
		require.Len(s.T(), entries, 1)
	})
}

// A link swapped in between a check and the open must not redirect it: a
// goroutine keeps renaming a plain file and a link out over demo/swap while
// it's read, served and written.
func (s *ServerSuite) TestPlaygroundSymlinkSpecSwap() {
	chans := s.specChannels()
	sp := s.setupPlaygroundSpec(chans)
	demo := filepath.Join(s.srv.loopDir, "playground", "demo")
	before := s.snapshot(sp.outside)

	done := make(chan struct{})
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			tmp := filepath.Join(demo, "swap.tmp")
			if i%2 == 0 {
				_ = os.Symlink(filepath.Join(sp.outside, hostMarker), tmp)
			} else {
				_ = os.WriteFile(tmp, []byte("plain"), 0o644)
			}
			_ = os.Rename(tmp, filepath.Join(demo, "swap"))
		}
	}()
	for range 300 {
		for _, op := range []struct{ method, url, body string }{
			{"GET", "/api/playground/file?name=demo&path=swap", ""},
			{"GET", "/api/playground/serve/demo/swap", ""},
			{"PUT", "/api/playground/file?name=demo&path=swap", "pwned"},
		} {
			rec := s.testRequest(op.method, op.url, op.body)
			require.NotContains(s.T(), rec.Body.String(), hostMarker, op.method+" "+op.url)
		}
	}
	close(done)
	<-swapped
	require.Equal(s.T(), before, s.snapshot(sp.outside), "touched a host file")
}

// A playground base that can't be made fails the write with 500.
func (s *ServerSuite) TestPlaygroundBaseUnmakeable() {
	file := filepath.Join(s.realTempDir(), "file")
	require.NoError(s.T(), os.WriteFile(file, nil, 0o644))
	s.srv.loopDir = file
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: filepath.Join(file, "project")}, nil)
	for _, q := range []string{"", "&scope=project&channel_id=ch1"} {
		rec := s.testRequest("PUT", "/api/playground?name=demo"+q, `{"html":"x"}`)
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code, q)
	}
}
