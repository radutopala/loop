package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
)

const (
	repoOwner = "radutopala"
	repoName  = "loop"
)

var (
	httpGet          = http.Get
	osExecutable     = os.Executable
	filepathSymlinks = filepath.EvalSymlinks
	osChmod          = os.Chmod
	osRename         = os.Rename
	osRemove         = os.Remove
	osCreateTemp     = os.CreateTemp
	releasesURL      = fmt.Sprintf("https://github.com/%s/%s/releases/latest", repoOwner, repoName)

	getLatestVersionFunc = getLatestVersion
)

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "update",
		Aliases: []string{"u"},
		Short:   "Update loop to the latest version",
		RunE: func(_ *cobra.Command, _ []string) error {
			return doUpdate()
		},
	}
}

func doUpdate() error {
	latestVersion, err := getLatestVersionFunc()
	if err != nil {
		return fmt.Errorf("failed to get latest version: %w", err)
	}

	currentVersion := version
	if currentVersion == "dev" {
		currentVersion = "0.0.0"
	}

	current, err := semver.NewVersion(currentVersion)
	if err != nil {
		return fmt.Errorf("failed to parse current version: %w", err)
	}

	latest, err := semver.NewVersion(latestVersion)
	if err != nil {
		return fmt.Errorf("failed to parse latest version: %w", err)
	}

	if !latest.GreaterThan(current) {
		fmt.Printf("Current version %s is up to date\n", version)
		return nil
	}

	fmt.Printf("Updating from %s to %s...\n", version, latestVersion)

	exe, err := osExecutable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exe, err = filepathSymlinks(exe)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	if err := downloadAndReplace(latestVersion, exe); err != nil {
		return fmt.Errorf("failed to update: %w", err)
	}

	fmt.Printf("Successfully updated to %s\n", latestVersion)
	fmt.Printf("  OS:   %s\n", runtime.GOOS)
	fmt.Printf("  Arch: %s\n", runtime.GOARCH)

	return nil
}

func getLatestVersion() (string, error) {
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(releasesURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	parts := splitTag(location)
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected redirect location: %s", location)
	}

	v := parts[1]
	if len(v) > 0 && v[0] == 'v' {
		v = v[1:]
	}
	return v, nil
}

// splitTag splits a URL on "/tag/" to extract the version tag.
func splitTag(location string) []string {
	for i := 0; i < len(location)-5; i++ {
		if location[i:i+5] == "/tag/" {
			return []string{location[:i], location[i+5:]}
		}
	}
	return []string{location}
}

func downloadAndReplace(ver, exePath string) error {
	url := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/v%s/loop_%s_%s_%s.tar.gz",
		repoOwner, repoName, ver, ver, runtime.GOOS, runtime.GOARCH,
	)

	resp, err := httpGet(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	tmpFile, err := osCreateTemp("", "loop-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = osRemove(tmpPath) }()

	if err := extractTarGz(resp.Body, tmpFile); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	if err := osChmod(tmpPath, 0755); err != nil {
		return err
	}

	oldPath := exePath + ".old"
	if err := osRename(exePath, oldPath); err != nil {
		return err
	}

	if err := osRename(tmpPath, exePath); err != nil {
		_ = osRename(oldPath, exePath)
		return err
	}

	_ = osRemove(oldPath)

	return nil
}

func extractTarGz(r io.Reader, w io.Writer) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == "loop" {
			_, err := io.Copy(w, tr)
			return err
		}
	}

	return fmt.Errorf("loop binary not found in archive")
}
