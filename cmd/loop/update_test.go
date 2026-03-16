package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/testutil"
)

type UpdateSuite struct {
	suite.Suite
	app *app
	sys *testutil.MockSystem
}

func TestUpdateSuite(t *testing.T) {
	suite.Run(t, new(UpdateSuite))
}

func (s *UpdateSuite) SetupTest() {
	s.app = newApp()
	s.sys = newPassthroughMock()
	s.app.sys = s.sys
}

func (s *UpdateSuite) TestDoUpdateAlreadyUpToDate() {
	s.app.version = "1.0.0"
	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }

	err := s.app.doUpdate()
	require.NoError(s.T(), err)
}

func (s *UpdateSuite) TestDoUpdateDevVersion() {
	s.app.version = "dev"
	tmpDir := s.T().TempDir()

	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }
	s.sys.Override("Executable").Return(exePath, nil)
	evalCall := s.sys.Override("EvalSymlinks", mock.Anything).Return("", nil)
	evalCall.RunFn = func(args mock.Arguments) {
		evalCall.ReturnArguments = mock.Arguments{args.String(0), nil}
	}

	archive := createTestTarGz(s.T(), "loop", "new binary content")
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	err := s.app.doUpdate()
	require.NoError(s.T(), err)

	content, err := os.ReadFile(exePath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new binary content", string(content))
}

func (s *UpdateSuite) TestDoUpdateNewVersionAvailable() {
	s.app.version = "0.9.0"
	tmpDir := s.T().TempDir()

	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }
	s.sys.Override("Executable").Return(exePath, nil)
	evalCall := s.sys.Override("EvalSymlinks", mock.Anything).Return("", nil)
	evalCall.RunFn = func(args mock.Arguments) {
		evalCall.ReturnArguments = mock.Arguments{args.String(0), nil}
	}

	archive := createTestTarGz(s.T(), "loop", "updated binary")
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	err := s.app.doUpdate()
	require.NoError(s.T(), err)

	content, err := os.ReadFile(exePath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "updated binary", string(content))
}

func (s *UpdateSuite) TestDoUpdateGetLatestVersionError() {
	s.app.version = "1.0.0"
	s.app.getLatestVersionFn = func() (string, error) { return "", errors.New("network error") }

	err := s.app.doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to get latest version")
}

func (s *UpdateSuite) TestDoUpdateBadCurrentVersion() {
	s.app.version = "not-semver"
	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }

	err := s.app.doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to parse current version")
}

func (s *UpdateSuite) TestDoUpdateBadLatestVersion() {
	s.app.version = "1.0.0"
	s.app.getLatestVersionFn = func() (string, error) { return "not-semver", nil }

	err := s.app.doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to parse latest version")
}

func (s *UpdateSuite) TestDoUpdateExecutableError() {
	s.app.version = "0.9.0"
	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }
	s.sys.Override("Executable").Return("", errors.New("exe error"))

	err := s.app.doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to get executable path")
}

func (s *UpdateSuite) TestDoUpdateSymlinksError() {
	s.app.version = "0.9.0"
	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }
	s.sys.Override("Executable").Return("/tmp/loop", nil)
	s.sys.Override("EvalSymlinks", mock.Anything).Return("", errors.New("symlink error"))

	err := s.app.doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to resolve symlinks")
}

func (s *UpdateSuite) TestDoUpdateDownloadError() {
	s.app.version = "0.9.0"
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }
	s.sys.Override("Executable").Return(exePath, nil)
	evalCall := s.sys.Override("EvalSymlinks", mock.Anything).Return("", nil)
	evalCall.RunFn = func(args mock.Arguments) {
		evalCall.ReturnArguments = mock.Arguments{args.String(0), nil}
	}
	s.app.httpGet = func(_ string) (*http.Response, error) { return nil, errors.New("download error") }

	err := s.app.doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to update")
}

func (s *UpdateSuite) TestNewUpdateCmdRunE() {
	s.app.version = "1.0.0"
	s.app.getLatestVersionFn = func() (string, error) { return "1.0.0", nil }

	cmd := s.app.newUpdateCmd()
	err := cmd.RunE(cmd, nil)
	require.NoError(s.T(), err)
}

func (s *UpdateSuite) TestDownloadAndReplaceHTTPError() {
	s.app.httpGet = func(_ string) (*http.Response, error) { return nil, errors.New("connection refused") }

	err := s.app.downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connection refused")
}

func (s *UpdateSuite) TestDownloadAndReplaceNon200() {
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	err := s.app.downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "download failed")
}

func (s *UpdateSuite) TestDownloadAndReplaceTempFileError() {
	s.app.httpGet = func(_ string) (*http.Response, error) {
		archive := createTestTarGz(s.T(), "loop", "binary")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}
	s.sys.Override("CreateTemp", mock.Anything, mock.Anything).Return(nil, errors.New("temp error"))

	err := s.app.downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "temp error")
}

func (s *UpdateSuite) TestDownloadAndReplaceChmodError() {
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	archive := createTestTarGz(s.T(), "loop", "new")
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}
	s.sys.Override("Chmod", mock.Anything, mock.Anything).Return(errors.New("chmod error"))

	err := s.app.downloadAndReplace("1.0.0", exePath)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "chmod error")
}

func (s *UpdateSuite) TestDownloadAndReplaceRenameOldError() {
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/nonexistent"

	archive := createTestTarGz(s.T(), "loop", "new")
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	err := s.app.downloadAndReplace("1.0.0", exePath)
	require.Error(s.T(), err)
}

func (s *UpdateSuite) TestDownloadAndReplaceRenameNewErrorRestoresOld() {
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("original"), 0755))

	archive := createTestTarGz(s.T(), "loop", "new")
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	// First rename (old→old.old) succeeds with real OS rename
	s.sys.Override("Rename", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		_ = os.Rename(args.String(0), args.String(1))
	})
	// Second rename (tmp→exe) fails
	s.sys.On("Rename", mock.Anything, mock.Anything).Return(errors.New("rename new error")).Once()
	// Third rename (rollback: old.old→exe) succeeds with real OS rename
	s.sys.On("Rename", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		_ = os.Rename(args.String(0), args.String(1))
	})

	err := s.app.downloadAndReplace("1.0.0", exePath)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "rename new error")

	// Original binary should be restored
	content, err := os.ReadFile(exePath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "original", string(content))
}

func (s *UpdateSuite) TestNewUpdateCmd() {
	cmd := s.app.newUpdateCmd()
	require.Equal(s.T(), "update", cmd.Use)
	require.Contains(s.T(), cmd.Aliases, "u")
}

// --- Tests for extractTarGz ---

func TestExtractTarGz(t *testing.T) {
	tests := []struct {
		name          string
		createArchive func(t *testing.T) []byte
		wantContent   string
		wantErr       string
	}{
		{
			name: "extracts loop binary",
			createArchive: func(t *testing.T) []byte {
				return createTestTarGz(t, "loop", "loop binary content")
			},
			wantContent: "loop binary content",
		},
		{
			name: "extracts loop binary from nested path",
			createArchive: func(t *testing.T) []byte {
				return createTestTarGzWithPath(t, "loop_1.0.0_darwin_arm64/loop", "nested loop binary")
			},
			wantContent: "nested loop binary",
		},
		{
			name: "returns error when loop binary not found",
			createArchive: func(t *testing.T) []byte {
				return createTestTarGz(t, "other-file", "other content")
			},
			wantErr: "loop binary not found in archive",
		},
		{
			name: "skips directories",
			createArchive: func(t *testing.T) []byte {
				var buf bytes.Buffer
				gw := gzip.NewWriter(&buf)
				tw := tar.NewWriter(gw)

				dirHdr := &tar.Header{
					Name:     "loop/",
					Mode:     0755,
					Typeflag: tar.TypeDir,
				}
				require.NoError(t, tw.WriteHeader(dirHdr))

				content := []byte("actual binary")
				hdr := &tar.Header{
					Name:     "loop/loop",
					Mode:     0755,
					Size:     int64(len(content)),
					Typeflag: tar.TypeReg,
				}
				require.NoError(t, tw.WriteHeader(hdr))
				_, err := tw.Write(content)
				require.NoError(t, err)
				require.NoError(t, tw.Close())
				require.NoError(t, gw.Close())

				return buf.Bytes()
			},
			wantContent: "actual binary",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := tc.createArchive(t)
			var out bytes.Buffer
			err := extractTarGz(bytes.NewReader(archive), &out)

			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantContent, out.String())
			}
		})
	}
}

func TestExtractTarGzInvalidGzip(t *testing.T) {
	var out bytes.Buffer
	err := extractTarGz(bytes.NewReader([]byte("not a gzip")), &out)
	require.Error(t, err)
}

// --- Tests for splitTag ---

func TestSplitTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"standard release URL", "https://github.com/radutopala/loop/releases/tag/v0.1.0", []string{"https://github.com/radutopala/loop/releases", "v0.1.0"}},
		{"no tag in URL", "https://github.com/radutopala/loop/releases", []string{"https://github.com/radutopala/loop/releases"}},
		{"empty string", "", []string{""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := splitTag(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// --- Test helpers ---

func createTestTarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	return createTestTarGzWithPath(t, name, content)
}

func createTestTarGzWithPath(t *testing.T, path, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	data := []byte(content)
	hdr := &tar.Header{
		Name:     path,
		Mode:     0755,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	return buf.Bytes()
}

func (s *UpdateSuite) TestDownloadAndReplaceExtractError() {
	s.app.httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte("not a tarball"))),
		}, nil
	}

	err := s.app.downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
}

// --- Tests for getLatestVersion ---

func (s *UpdateSuite) TestGetLatestVersionRedirect() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/radutopala/loop/releases/tag/v1.2.3")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	v, err := getLatestVersion(srv.URL)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1.2.3", v)
}

func (s *UpdateSuite) TestGetLatestVersionNoVPrefix() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/radutopala/loop/releases/tag/2.0.0")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()

	v, err := getLatestVersion(srv.URL)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "2.0.0", v)
}

func (s *UpdateSuite) TestGetLatestVersionUnexpectedStatus() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := getLatestVersion(srv.URL)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unexpected status")
}

func (s *UpdateSuite) TestGetLatestVersionBadLocation() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://example.com/no-tag-here")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	_, err := getLatestVersion(srv.URL)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unexpected redirect location")
}

func (s *UpdateSuite) TestGetLatestVersionNetworkError() {
	_, err := getLatestVersion("http://127.0.0.1:0/nonexistent")
	require.Error(s.T(), err)
}

func TestExtractTarGzCorruptedTar(t *testing.T) {
	// Valid gzip wrapping corrupt tar data
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte("this is not valid tar data"))
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	var out bytes.Buffer
	err = extractTarGz(bytes.NewReader(buf.Bytes()), &out)
	require.Error(t, err)
}
