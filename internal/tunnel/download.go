package tunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
)

// binaryName is the on-disk cloudflared filename under binDir. binExeSuffix is
// defined per-platform (proc_unix.go / proc_windows.go).
func binaryName() string {
	return "cloudflared" + binExeSuffix
}

// ensureBinary returns the path to a verified cloudflared binary, downloading
// it on first use. If a binary already exists at the expected path it is used
// as-is (re-verification on every start would be wasteful; integrity is
// enforced at download time).
func (m *Manager) ensureBinary() (string, error) {
	binPath := filepath.Join(m.binDir, binaryName())
	if _, err := m.statFile(binPath); err == nil {
		return binPath, nil
	}

	key := runtime.GOOS + "/" + runtime.GOARCH
	a, ok := assetChecksums[key]
	if !ok {
		return "", fmt.Errorf("cloudflared is not supported on %s", key)
	}

	if err := m.mkdirAll(m.binDir, 0o755); err != nil {
		return "", fmt.Errorf("creating bin dir: %w", err)
	}

	url := fmt.Sprintf(
		"https://github.com/cloudflare/cloudflared/releases/download/%s/%s",
		pinnedVersion, a.name,
	)
	m.logger.Info("downloading cloudflared", "version", pinnedVersion, "asset", a.name)

	resp, err := m.httpGet(url)
	if err != nil {
		return "", fmt.Errorf("downloading cloudflared: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading cloudflared: %s", resp.Status)
	}

	// Read the whole asset so we can verify its checksum before trusting it.
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading cloudflared download: %w", err)
	}

	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != a.sha256 {
		return "", fmt.Errorf("cloudflared checksum mismatch: expected %s, got %s", a.sha256, got)
	}

	binBytes := raw
	if a.isTarGz {
		binBytes, err = extractCloudflared(raw)
		if err != nil {
			return "", err
		}
	}

	// Atomic install: write to a temp file, chmod, rename into place.
	tmp := binPath + ".tmp"
	if err := m.writeFile(tmp, binBytes, 0o755); err != nil {
		return "", fmt.Errorf("writing cloudflared: %w", err)
	}
	if err := m.chmod(tmp, 0o755); err != nil {
		_ = m.removeFile(tmp)
		return "", fmt.Errorf("chmod cloudflared: %w", err)
	}
	if err := m.rename(tmp, binPath); err != nil {
		_ = m.removeFile(tmp)
		return "", fmt.Errorf("installing cloudflared: %w", err)
	}
	m.logger.Info("cloudflared installed", "path", binPath)
	return binPath, nil
}

// extractCloudflared pulls the cloudflared binary out of a .tgz asset (macOS).
func extractCloudflared(tgz []byte) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("gunzip cloudflared: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading cloudflared archive: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "cloudflared" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("extracting cloudflared: %w", err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("cloudflared binary not found in archive")
}
