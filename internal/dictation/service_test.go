package dictation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/localserver"
)

type mockLocalServer struct{ mock.Mock }

func (m *mockLocalServer) FindBinary() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockLocalServer) Acquire(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
}

func (m *mockLocalServer) Release()      { m.Called() }
func (m *mockLocalServer) Running() bool { return m.Called().Bool(0) }
func (m *mockLocalServer) Stop()         { m.Called() }

type ServiceSuite struct {
	suite.Suite
	server *mockLocalServer
	dl     *Downloader
	cfg    Config
	svc    *Service
	ts     *httptest.Server
}

func TestServiceSuite(t *testing.T) { suite.Run(t, new(ServiceSuite)) }

func (s *ServiceSuite) SetupTest() {
	s.server = new(mockLocalServer)
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/inference" {
			_, _ = w.Write([]byte(`{"text":"hi"}`))
			return
		}
		http.NotFound(w, r)
	}))
	s.T().Cleanup(s.ts.Close)
	s.dl = NewDownloader(s.T().TempDir(), s.ts.Client(), slog.New(slog.DiscardHandler))
	s.dl.baseURL = s.ts.URL + "/"
	s.cfg = Config{Enabled: true, Model: "large-v3-turbo-q5_0", Language: "auto", Threads: 3}
	s.svc = NewService(context.Background(), func() Config { return s.cfg }, s.server, s.dl, NewEngine(s.ts.Client()))
}

// installModel puts a file of the model's size where the downloader keeps it.
func (s *ServiceSuite) installModel(name string) string {
	m, _ := LookupModel(name)
	path := s.dl.Path(m)
	f, err := os.Create(path)
	require.NoError(s.T(), err)
	require.NoError(s.T(), f.Truncate(m.Size))
	require.NoError(s.T(), f.Close())
	return path
}

func (s *ServiceSuite) TestStatus() {
	tests := []struct {
		name    string
		model   string
		missing bool
		want    Status
	}{
		{
			name:  "ready to download",
			model: "large-v3-turbo",
			want:  Status{Available: true, Model: "large-v3-turbo", ModelSizeBytes: 1624555275},
		},
		{
			name:    "no binary, unknown model is the default",
			model:   "nope",
			missing: true,
			want:    Status{Reason: CodeBinaryMissing, Model: "large-v3-turbo-q5_0", ModelSizeBytes: 574041195},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			// Turned off, so Status doesn't start the download.
			s.cfg.Enabled = false
			s.cfg.Model = tc.model
			s.server.On("Running").Return(false)
			if tc.missing {
				s.server.On("FindBinary").Return("", localserver.ErrBinaryMissing)
			} else {
				s.server.On("FindBinary").Return("/bin/whisper-server", nil)
			}
			require.Equal(s.T(), tc.want, s.svc.Status())
		})
	}
}

func (s *ServiceSuite) TestStatusModelReady() {
	s.installModel("large-v3-turbo-q5_0")
	s.server.On("Running").Return(true)
	s.server.On("FindBinary").Return("/bin/whisper-server", nil)
	st := s.svc.Status()
	require.True(s.T(), st.Enabled)
	require.True(s.T(), st.ModelReady)
	require.True(s.T(), st.Running)
	require.False(s.T(), st.Download.Active)
}

// downloadFailed waits for the background download to end on the test
// server's 404.
func (s *ServiceSuite) downloadFailed() {
	require.Eventually(s.T(), func() bool {
		p := s.dl.Progress()
		return !p.Active && p.Error != ""
	}, 5*time.Second, time.Millisecond)
}

// Enabled with the model missing, Status starts the download.
func (s *ServiceSuite) TestStatusStartsDownloadWhenEnabled() {
	s.server.On("Running").Return(false)
	s.server.On("FindBinary").Return("/bin/whisper-server", nil)
	st := s.svc.Status()
	require.True(s.T(), st.Download.Active)
	s.downloadFailed()
}

func (s *ServiceSuite) TestEnsureModel() {
	tests := []struct {
		name    string
		enabled bool
		install bool
		failed  bool
		starts  bool
	}{
		{name: "enabled, model missing", enabled: true, starts: true},
		{name: "turned off", enabled: false},
		{name: "model there", enabled: true, install: true},
		{name: "last download failed: not retried on its own", enabled: true, failed: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.cfg.Enabled = tc.enabled
			if tc.install {
				s.installModel("large-v3-turbo-q5_0")
			}
			if tc.failed {
				done := make(chan struct{})
				require.True(s.T(), s.svc.DownloadModel(done))
				<-done
			}
			s.svc.EnsureModel()
			require.Equal(s.T(), tc.starts, s.dl.Progress().Active)
			if tc.starts {
				s.downloadFailed()
			}
		})
	}
}

func (s *ServiceSuite) TestRunAutoDownload() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.svc.RunAutoDownload(ctx, time.Millisecond)
		close(done)
	}()
	s.downloadFailed()
	// Let a few ticks pass: the failed download isn't retried.
	time.Sleep(10 * time.Millisecond)
	require.False(s.T(), s.dl.Progress().Active)
	cancel()
	<-done
}

func (s *ServiceSuite) TestDownloadModel() {
	done := make(chan struct{})
	require.True(s.T(), s.svc.DownloadModel(done))
	<-done
	// The test server has no model, so the download fails.
	require.Contains(s.T(), s.dl.Progress().Error, "HTTP 404")
}

func (s *ServiceSuite) TestBeginErrors() {
	tests := []struct {
		name    string
		install bool
		err     error
		code    string
	}{
		{name: "turned off", code: CodeDisabled},
		{name: "model missing", code: CodeModelMissing},
		{name: "binary missing", install: true, err: fmt.Errorf("x: %w", localserver.ErrBinaryMissing), code: CodeBinaryMissing},
		{name: "server fails", install: true, err: errors.New("crashed"), code: CodeEngineFailed},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.cfg.Enabled = tc.code != CodeDisabled
			if tc.install {
				s.installModel("large-v3-turbo-q5_0")
				s.server.On("Stop").Return()
				s.server.On("Acquire", mock.Anything).Return("", tc.err)
			}
			_, _, err := s.svc.Begin(context.Background(), "", func(Event) {})
			var derr *Error
			require.ErrorAs(s.T(), err, &derr)
			require.Equal(s.T(), tc.code, derr.Code)
			require.NotEmpty(s.T(), derr.Error())
			if tc.err != nil {
				require.ErrorIs(s.T(), err, tc.err)
			}
		})
	}
}

// Begin starts the server with the configured model, restarting it when the
// model changed, and the session transcribes through it.
func (s *ServiceSuite) TestBegin() {
	q5 := s.installModel("large-v3-turbo-q5_0")
	full := s.installModel("large-v3-turbo")
	s.server.On("Stop").Return().Once()
	s.server.On("Acquire", mock.Anything).Return(s.ts.URL, nil)
	s.server.On("Release").Return()

	sess, release, err := s.svc.Begin(context.Background(), "", func(Event) {})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "auto", sess.language)
	require.Equal(s.T(), []string{"-m", q5, "--host", "127.0.0.1", "--port", "7", "-bs", "1", "-bo", "1", "-nt", "-sns", "-t", "3"}, s.svc.ServerArgs(7))
	text, err := sess.transcribe(context.Background(), nil, "en", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi", text)
	release()

	sess, _, err = s.svc.Begin(context.Background(), "ro", func(Event) {})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ro", sess.language)
	s.server.AssertNumberOfCalls(s.T(), "Stop", 1)

	s.cfg.Model = "large-v3-turbo"
	s.server.On("Stop").Return().Once()
	_, _, err = s.svc.Begin(context.Background(), "", func(Event) {})
	require.NoError(s.T(), err)
	s.server.AssertNumberOfCalls(s.T(), "Stop", 2)
	require.Equal(s.T(), full, s.svc.ServerArgs(7)[1])
	require.Equal(s.T(), filepath.Base(full), "ggml-large-v3-turbo.bin")
}
