package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"filippo.io/age"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/backup"
)

type mockBackupRunner struct {
	mock.Mock
}

func (m *mockBackupRunner) Status() backup.Status {
	return m.Called().Get(0).(backup.Status)
}

func (m *mockBackupRunner) Start() error {
	return m.Called().Error(0)
}

func (s *ServerSuite) TestBackupNotConfigured() {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, httptest.NewRequest(method, "/api/backup", nil))
		require.Equal(s.T(), http.StatusNotImplemented, w.Code, method)
	}
}

func (s *ServerSuite) TestGetBackup() {
	runner := new(mockBackupRunner)
	runner.On("Status").Return(backup.Status{Enabled: true, Dir: "/backups", TimesPerDay: 2, KeepLast: 7, Count: 3, TotalSize: 42})
	s.srv.SetBackup(runner)

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backup", nil))
	require.Equal(s.T(), http.StatusOK, w.Code)
	var got backup.Status
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(s.T(), backup.Status{Enabled: true, Dir: "/backups", TimesPerDay: 2, KeepLast: 7, Count: 3, TotalSize: 42}, got)
}

func (s *ServerSuite) TestStartBackup() {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"started", nil, http.StatusAccepted},
		{"already running", backup.ErrRunning, http.StatusConflict},
		{"no path", backup.ErrNoPath, http.StatusBadRequest},
		{"bad settings", errors.New("the backup path \"x\" isn't absolute"), http.StatusBadRequest},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			runner := new(mockBackupRunner)
			runner.On("Start").Return(tc.err)
			s.srv.SetBackup(runner)

			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/backup", nil))
			require.Equal(s.T(), tc.code, w.Code)
			runner.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestCreateBackupKey() {
	dir := s.writeLoopConfig("{\n  // my settings\n  \"backup\": {\n    \"enabled\": true\n  }\n}\n")

	rec := s.testRequest(http.MethodPost, "/api/backup/key", "")
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	var got backupKeyResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&got))

	// The keys are a pair: what's encrypted to the public key decrypts
	// with the private one.
	recipient, err := age.ParseX25519Recipient(got.Recipient)
	require.NoError(s.T(), err)
	identity, err := age.ParseX25519Identity(got.Identity)
	require.NoError(s.T(), err)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	require.NoError(s.T(), err)
	_, err = w.Write([]byte("hi"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), w.Close())
	r, err := age.Decrypt(&buf, identity)
	require.NoError(s.T(), err)
	data, err := io.ReadAll(r)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi", string(data))

	// The public key is in the config, its comments kept; the private key
	// isn't anywhere.
	cfg, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(cfg), "// my settings")
	require.Contains(s.T(), string(cfg), got.Recipient)
	require.NotContains(s.T(), string(cfg), "AGE-SECRET-KEY")
}

func (s *ServerSuite) TestCreateBackupKeyFailures() {
	s.Run("no loop dir", func() {
		s.srv.loopDir = ""
		rec := s.testRequest(http.MethodPost, "/api/backup/key", "")
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	})
	s.Run("generating", func() {
		s.writeLoopConfig("{}")
		s.srv.newBackupKey = func() (*age.X25519Identity, error) { return nil, errors.New("no randomness") }
		defer func() { s.srv.newBackupKey = age.GenerateX25519Identity }()
		rec := s.testRequest(http.MethodPost, "/api/backup/key", "")
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "generating the key: no randomness")
	})
	s.Run("saving", func() {
		s.writeLoopConfig("{ not hjson")
		rec := s.testRequest(http.MethodPost, "/api/backup/key", "")
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "saving the public key")
	})
}
