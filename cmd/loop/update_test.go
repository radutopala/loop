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

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type UpdateSuite struct {
	suite.Suite
	origVersion              string
	origGetLatestVersionFunc func() (string, error)
	origOsExecutable         func() (string, error)
	origFilepathSymlinks     func(string) (string, error)
	origHttpGet              func(string) (*http.Response, error)
	origOsChmod              func(string, os.FileMode) error
	origOsRename             func(string, string) error
	origOsRemove             func(string) error
	origOsCreateTemp         func(string, string) (*os.File, error)
	origReleasesURL          string
}

func TestUpdateSuite(t *testing.T) {
	suite.Run(t, new(UpdateSuite))
}

func (s *UpdateSuite) SetupTest() {
	s.origVersion = version
	s.origGetLatestVersionFunc = getLatestVersionFunc
	s.origOsExecutable = osExecutable
	s.origFilepathSymlinks = filepathSymlinks
	s.origHttpGet = httpGet
	s.origOsChmod = osChmod
	s.origOsRename = osRename
	s.origOsRemove = osRemove
	s.origOsCreateTemp = osCreateTemp
	s.origReleasesURL = releasesURL
}

func (s *UpdateSuite) TearDownTest() {
	version = s.origVersion
	getLatestVersionFunc = s.origGetLatestVersionFunc
	osExecutable = s.origOsExecutable
	filepathSymlinks = s.origFilepathSymlinks
	httpGet = s.origHttpGet
	osChmod = s.origOsChmod
	osRename = s.origOsRename
	osRemove = s.origOsRemove
	osCreateTemp = s.origOsCreateTemp
	releasesURL = s.origReleasesURL
}

func (s *UpdateSuite) TestDoUpdateAlreadyUpToDate() {
	version = "1.0.0"
	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }

	err := doUpdate()
	require.NoError(s.T(), err)
}

func (s *UpdateSuite) TestDoUpdateDevVersion() {
	version = "dev"
	tmpDir := s.T().TempDir()

	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }
	osExecutable = func() (string, error) { return exePath, nil }
	filepathSymlinks = func(p string) (string, error) { return p, nil }

	archive := createTestTarGz(s.T(), "loop", "new binary content")
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	err := doUpdate()
	require.NoError(s.T(), err)

	content, err := os.ReadFile(exePath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new binary content", string(content))
}

func (s *UpdateSuite) TestDoUpdateNewVersionAvailable() {
	version = "0.9.0"
	tmpDir := s.T().TempDir()

	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }
	osExecutable = func() (string, error) { return exePath, nil }
	filepathSymlinks = func(p string) (string, error) { return p, nil }

	archive := createTestTarGz(s.T(), "loop", "updated binary")
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	err := doUpdate()
	require.NoError(s.T(), err)

	content, err := os.ReadFile(exePath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "updated binary", string(content))
}

func (s *UpdateSuite) TestDoUpdateGetLatestVersionError() {
	version = "1.0.0"
	getLatestVersionFunc = func() (string, error) { return "", errors.New("network error") }

	err := doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to get latest version")
}

func (s *UpdateSuite) TestDoUpdateBadCurrentVersion() {
	version = "not-semver"
	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }

	err := doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to parse current version")
}

func (s *UpdateSuite) TestDoUpdateBadLatestVersion() {
	version = "1.0.0"
	getLatestVersionFunc = func() (string, error) { return "not-semver", nil }

	err := doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to parse latest version")
}

func (s *UpdateSuite) TestDoUpdateExecutableError() {
	version = "0.9.0"
	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }
	osExecutable = func() (string, error) { return "", errors.New("exe error") }

	err := doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to get executable path")
}

func (s *UpdateSuite) TestDoUpdateSymlinksError() {
	version = "0.9.0"
	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }
	osExecutable = func() (string, error) { return "/tmp/loop", nil }
	filepathSymlinks = func(_ string) (string, error) { return "", errors.New("symlink error") }

	err := doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to resolve symlinks")
}

func (s *UpdateSuite) TestDoUpdateDownloadError() {
	version = "0.9.0"
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }
	osExecutable = func() (string, error) { return exePath, nil }
	filepathSymlinks = func(p string) (string, error) { return p, nil }
	httpGet = func(_ string) (*http.Response, error) { return nil, errors.New("download error") }

	err := doUpdate()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "failed to update")
}

func (s *UpdateSuite) TestNewUpdateCmdRunE() {
	version = "1.0.0"
	getLatestVersionFunc = func() (string, error) { return "1.0.0", nil }

	cmd := newUpdateCmd()
	err := cmd.RunE(cmd, nil)
	require.NoError(s.T(), err)
}

func (s *UpdateSuite) TestDownloadAndReplaceHTTPError() {
	httpGet = func(_ string) (*http.Response, error) { return nil, errors.New("connection refused") }

	err := downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connection refused")
}

func (s *UpdateSuite) TestDownloadAndReplaceNon200() {
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	err := downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "download failed")
}

func (s *UpdateSuite) TestDownloadAndReplaceTempFileError() {
	httpGet = func(_ string) (*http.Response, error) {
		archive := createTestTarGz(s.T(), "loop", "binary")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}
	osCreateTemp = func(_, _ string) (*os.File, error) { return nil, errors.New("temp error") }

	err := downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "temp error")
}

func (s *UpdateSuite) TestDownloadAndReplaceChmodError() {
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("old"), 0755))

	archive := createTestTarGz(s.T(), "loop", "new")
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}
	osChmod = func(_ string, _ os.FileMode) error { return errors.New("chmod error") }

	err := downloadAndReplace("1.0.0", exePath)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "chmod error")
}

func (s *UpdateSuite) TestDownloadAndReplaceRenameOldError() {
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/nonexistent"

	archive := createTestTarGz(s.T(), "loop", "new")
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	err := downloadAndReplace("1.0.0", exePath)
	require.Error(s.T(), err)
}

func (s *UpdateSuite) TestDownloadAndReplaceRenameNewErrorRestoresOld() {
	tmpDir := s.T().TempDir()
	exePath := tmpDir + "/loop"
	require.NoError(s.T(), os.WriteFile(exePath, []byte("original"), 0755))

	archive := createTestTarGz(s.T(), "loop", "new")
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}

	// First rename (old) succeeds, second rename (new->exe) fails
	callCount := 0
	origRename := s.origOsRename
	osRename = func(src, dst string) error {
		callCount++
		if callCount == 2 {
			return errors.New("rename new error")
		}
		return origRename(src, dst)
	}

	err := downloadAndReplace("1.0.0", exePath)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "rename new error")

	// Original binary should be restored
	content, err := os.ReadFile(exePath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "original", string(content))
}

func (s *UpdateSuite) TestNewUpdateCmd() {
	cmd := newUpdateCmd()
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
	httpGet = func(_ string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte("not a tarball"))),
		}, nil
	}

	err := downloadAndReplace("1.0.0", "/tmp/loop")
	require.Error(s.T(), err)
}

// --- Tests for getLatestVersion ---

func (s *UpdateSuite) TestGetLatestVersionRedirect() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/radutopala/loop/releases/tag/v1.2.3")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	releasesURL = srv.URL

	v, err := getLatestVersion()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "1.2.3", v)
}

func (s *UpdateSuite) TestGetLatestVersionMovedPermanently() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/radutopala/loop/releases/tag/v2.0.0")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()

	releasesURL = srv.URL

	v, err := getLatestVersion()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "2.0.0", v)
}

func (s *UpdateSuite) TestGetLatestVersionUnexpectedStatus() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	releasesURL = srv.URL

	_, err := getLatestVersion()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unexpected status")
}

func (s *UpdateSuite) TestGetLatestVersionBadRedirectLocation() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/radutopala/loop/releases")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	releasesURL = srv.URL

	_, err := getLatestVersion()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unexpected redirect location")
}

func (s *UpdateSuite) TestGetLatestVersionNetworkError() {
	releasesURL = "http://127.0.0.1:0/nonexistent"

	_, err := getLatestVersion()
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
