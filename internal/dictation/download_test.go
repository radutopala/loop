package dictation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
)

const modelBody = "fake model weights"

func testModel() Model {
	sum := sha256.Sum256([]byte(modelBody))
	return Model{Name: "test", Size: int64(len(modelBody)), SHA256: hex.EncodeToString(sum[:])}
}

type failingClient struct{}

func (failingClient) Do(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }

type DownloadSuite struct {
	suite.Suite
	dir  string
	ts   *httptest.Server
	dl   *Downloader
	hits int
}

func TestDownloadSuite(t *testing.T) { suite.Run(t, new(DownloadSuite)) }

func (s *DownloadSuite) SetupTest() {
	s.hits = 0
	s.dir = filepath.Join(s.T().TempDir(), "models")
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits++
		if r.URL.Path != "/ggml-test.bin" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(modelBody))
	}))
	s.T().Cleanup(s.ts.Close)
	s.dl = NewDownloader(s.dir, s.ts.Client(), slog.New(slog.DiscardHandler))
	s.dl.baseURL = s.ts.URL + "/"
}

// download runs a download of m to its end.
func (s *DownloadSuite) download(m Model) Progress {
	done := make(chan struct{})
	require.True(s.T(), s.dl.Start(context.Background(), m, done))
	<-done
	return s.dl.Progress()
}

func (s *DownloadSuite) TestCatalog() {
	m, ok := LookupModel(config.DefaultDictationModel)
	require.True(s.T(), ok)
	require.Equal(s.T(), "ggml-large-v3-turbo-q5_0.bin", m.FileName())
	_, ok = LookupModel("tiny")
	require.False(s.T(), ok)
	// Every model the config offers can be downloaded.
	for _, name := range config.DictationModels() {
		_, ok := LookupModel(name)
		require.True(s.T(), ok, name)
	}
}

func (s *DownloadSuite) TestDownloads() {
	m := testModel()
	require.False(s.T(), s.dl.Ready(m))

	p := s.download(m)

	require.Equal(s.T(), Progress{Received: m.Size, Total: m.Size}, p)
	require.True(s.T(), s.dl.Ready(m))
	data, err := os.ReadFile(s.dl.Path(m))
	require.NoError(s.T(), err)
	require.Equal(s.T(), modelBody, string(data))
	require.NoFileExists(s.T(), s.dl.Path(m)+".part")

	require.False(s.T(), s.dl.Start(context.Background(), m, nil), "already downloaded")
	require.Equal(s.T(), 1, s.hits)
}

func (s *DownloadSuite) TestStartWhileActive() {
	m := testModel()
	s.dl.progress.Active = true
	require.False(s.T(), s.dl.Start(context.Background(), m, nil))
	s.dl.progress.Active = false

	// Without done, the download still runs to the end.
	require.True(s.T(), s.dl.Start(context.Background(), m, nil))
	require.Eventually(s.T(), func() bool { return s.dl.Ready(m) && !s.dl.Progress().Active }, testWait, testTick)
}

func (s *DownloadSuite) TestFailures() {
	tests := []struct {
		name  string
		setup func(m *Model)
		want  string
	}{
		{name: "checksum", setup: func(m *Model) { m.SHA256 = "00" }, want: "checksum mismatch"},
		{name: "size", setup: func(m *Model) { m.Size++ }, want: "checksum mismatch"},
		{name: "not found", setup: func(m *Model) { m.Name = "gone" }, want: "HTTP 404"},
		{name: "offline", setup: func(*Model) { s.dl.client = failingClient{} }, want: "offline"},
		{name: "bad url", setup: func(*Model) { s.dl.baseURL = "http://\x7f/" }, want: "invalid control character"},
		{
			name: "models dir is a file",
			setup: func(*Model) {
				require.NoError(s.T(), os.WriteFile(filepath.Dir(s.dir)+"/file", nil, 0o600))
				s.dl.dir = filepath.Dir(s.dir) + "/file/models"
			},
			want: "not a directory",
		},
		{
			name: "part file can't be created",
			setup: func(m *Model) {
				require.NoError(s.T(), os.MkdirAll(s.dl.Path(*m)+".part", 0o755))
			},
			want: "is a directory",
		},
		{
			name: "model path is taken",
			setup: func(m *Model) {
				require.NoError(s.T(), os.MkdirAll(filepath.Join(s.dl.Path(*m), "x"), 0o755))
			},
			want: "rename",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			m := testModel()
			tc.setup(&m)

			p := s.download(m)

			require.False(s.T(), p.Active)
			require.Contains(s.T(), p.Error, tc.want)
			require.False(s.T(), s.dl.Ready(m))
			if tc.name == "checksum" {
				require.NoFileExists(s.T(), s.dl.Path(m)+".part")
			}
		})
	}
}
