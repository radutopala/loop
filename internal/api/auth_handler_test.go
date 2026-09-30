package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/db"
)

type AuthHandlerSuite struct {
	suite.Suite
	srv     *Server
	store   *MockChannelLister
	caps    *apiauth.Signer
	dir     string
	handler http.Handler
}

func TestAuthHandlerSuite(t *testing.T) {
	suite.Run(t, new(AuthHandlerSuite))
}

func (s *AuthHandlerSuite) SetupTest() {
	var err error
	s.caps, err = apiauth.NewSigner()
	require.NoError(s.T(), err)
	s.store = new(MockChannelLister)
	s.dir = s.T().TempDir()
	s.srv = NewServer(nil, nil, nil, s.store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuth(AuthDeps{OwnerToken: "owner", Caps: s.caps}))
	s.srv.SetLoopDir(filepath.Join(s.dir, "loop"))

	proj := filepath.Join(s.dir, "proj")
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: proj}, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "ghost").Return(nil, nil).Maybe()
	s.write(filepath.Join(proj, "a.txt"), "hello")
	s.write(filepath.Join(proj, ".loop", "playground", "demo", "index.html"), "<p>project</p>")
	s.write(filepath.Join(proj, ".loop", "playground", "demo", "app.js"), "let x")
	s.write(filepath.Join(s.dir, "loop", "playground", "demo", "index.html"), "<p>global</p>")

	mux := http.NewServeMux()
	s.srv.registerSystemRoutes(mux)
	s.handler = s.srv.auth.Wrap(mux)
}

func (s *AuthHandlerSuite) write(path, content string) {
	require.NoError(s.T(), os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(s.T(), os.WriteFile(path, []byte(content), 0o600))
}

func (s *AuthHandlerSuite) do(tok, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func (s *AuthHandlerSuite) TestRotate() {
	s.Run("not configured", func() {
		require.Equal(s.T(), http.StatusNotImplemented, s.do("owner", "POST", "/api/auth/rotate", "").Code)
	})
	s.Run("error", func() {
		s.srv.rotateOwnerToken = func() (string, error) { return "", errors.New("disk full") }
		rec := s.do("owner", "POST", "/api/auth/rotate", "")
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "disk full")
	})
	s.Run("rotates", func() {
		s.srv.rotateOwnerToken = func() (string, error) { return "fresh", nil }
		require.Equal(s.T(), http.StatusNoContent, s.do("owner", "POST", "/api/auth/rotate", "").Code)
		require.Equal(s.T(), http.StatusUnauthorized, s.do("owner", "GET", "/api/auth/rotate", "").Code)
		require.Equal(s.T(), http.StatusNoContent, s.do("fresh", "POST", "/api/auth/rotate", "").Code)
	})
}

// mint asks for a content link and returns its base URL.
func (s *AuthHandlerSuite) mint(body string) string {
	rec := s.do("owner", "POST", "/api/content-caps", body)
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var resp contentCapResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), 3600, resp.ExpiresInSec)
	require.True(s.T(), strings.HasPrefix(resp.BaseURL, "/c/"))
	return resp.BaseURL
}

func (s *AuthHandlerSuite) TestCreateContentCapRejects() {
	tests := map[string]string{
		"bad json":            `nope`,
		"unknown kind":        `{"kind":"zip"}`,
		"raw without channel": `{"kind":"raw"}`,
		"playground no name":  `{"kind":"playground","channel_id":"ch1"}`,
	}
	for name, body := range tests {
		s.Run(name, func() {
			require.Equal(s.T(), http.StatusBadRequest, s.do("owner", "POST", "/api/content-caps", body).Code)
		})
	}
	s.Run("not configured", func() {
		s.srv.caps = nil
		require.Equal(s.T(), http.StatusNotImplemented, s.do("owner", "POST", "/api/content-caps", `{"kind":"raw"}`).Code)
	})
}

func (s *AuthHandlerSuite) TestServeRaw() {
	base := s.mint(`{"kind":"raw","channel_id":"ch1","root":0}`)

	rec := s.do("", "GET", base+"a.txt", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "hello", rec.Body.String())
	require.Equal(s.T(), "sandbox allow-scripts", rec.Header().Get("Content-Security-Policy"))

	require.Equal(s.T(), http.StatusNotFound, s.do("", "GET", base+"missing.txt", "").Code)
	require.Equal(s.T(), http.StatusBadRequest, s.do("", "GET", base+"..%2F..%2Fetc%2Fpasswd", "").Code)
}

func (s *AuthHandlerSuite) TestServePlayground() {
	project := s.mint(`{"kind":"playground","channel_id":"ch1","name":"demo"}`)
	rec := s.do("", "GET", project, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "<p>project</p>")
	require.Contains(s.T(), rec.Body.String(), `<base href="`+project+`">`)
	require.Equal(s.T(), playgroundCSP, rec.Header().Get("Content-Security-Policy"))

	rec = s.do("", "GET", project+"app.js", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "let x", rec.Body.String())
	require.Equal(s.T(), http.StatusNotFound, s.do("", "GET", project+"nope.js", "").Code)

	global := s.mint(`{"kind":"playground","name":"demo"}`)
	rec = s.do("", "GET", global, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "<p>global</p>")

	s.Run("unknown channel", func() {
		base := s.mint(`{"kind":"playground","channel_id":"ghost","name":"demo"}`)
		require.Equal(s.T(), http.StatusBadRequest, s.do("", "GET", base, "").Code)
	})
	s.Run("bad name", func() {
		base := s.mint(`{"kind":"playground","channel_id":"ch1","name":"../x"}`)
		require.Equal(s.T(), http.StatusBadRequest, s.do("", "GET", base, "").Code)
	})
}

func (s *AuthHandlerSuite) TestServeRejects() {
	s.Run("bad cap", func() {
		rec := s.do("", "GET", "/c/forged.sig/a.txt", "")
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "link expired or invalid")
	})
	s.Run("unknown kind", func() {
		tok := s.caps.Mint(apiauth.Cap{Kind: "zip"}, time.Minute)
		require.Equal(s.T(), http.StatusNotFound, s.do("", "GET", "/c/"+tok+"/a.txt", "").Code)
	})
	s.Run("not configured", func() {
		s.srv.caps = nil
		require.Equal(s.T(), http.StatusNotFound, s.do("", "GET", "/c/x/a.txt", "").Code)
	})
}

// A server started without WithAuth has no owner token, so it refuses
// every non-public request.
func (s *AuthHandlerSuite) TestStartFailsClosed() {
	srv := nilServer()
	require.NoError(s.T(), srv.Start("127.0.0.1:0"))
	defer func() { _ = srv.Stop(s.T().Context()) }()
	base := "http://" + srv.listener.Addr().String()

	get := func(path string, hdr ...string) int {
		req, err := http.NewRequestWithContext(s.T().Context(), "GET", base+path, nil)
		require.NoError(s.T(), err)
		if len(hdr) > 0 {
			req.Header.Set("Authorization", hdr[0])
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(s.T(), err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	require.Equal(s.T(), http.StatusOK, get("/api/health"))
	require.Equal(s.T(), http.StatusUnauthorized, get("/api/config"))
	require.Equal(s.T(), http.StatusUnauthorized, get("/api/config", "Bearer "))
}
