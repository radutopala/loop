// Package apiauth authenticates callers of the daemon's HTTP API. Every
// caller is either the owner (the user's desktop app, CLI and host tools,
// holding the owner token) or an agent (one container, holding a token the
// daemon issued it). Nothing is trusted ambiently: no cookies, no origin.
package apiauth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tokenBytes is the size of every token and key, before hex encoding.
const tokenBytes = 32

// OwnerTokenPath returns where the owner token lives: the OS's per-user
// config dir (~/Library/Application Support on macOS, ~/.config on Linux,
// %AppData% on Windows), deliberately outside ~/.loop, which agent
// containers may have mounted.
func OwnerTokenPath(userConfigDir func() (string, error)) (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating the config dir: %w", err)
	}
	return filepath.Join(dir, "loop", "api-token"), nil
}

// TokenFile reads and writes the owner token file.
type TokenFile struct {
	Path string

	readFile  func(string) ([]byte, error)
	writeFile func(string, []byte, os.FileMode) error
	mkdirAll  func(string, os.FileMode) error
	chmod     func(string, os.FileMode) error
	rename    func(string, string) error
	readRand  func([]byte) (int, error)
}

// NewTokenFile returns a TokenFile for path.
func NewTokenFile(path string) *TokenFile {
	return &TokenFile{
		Path:      path,
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
		chmod:     os.Chmod,
		rename:    os.Rename,
		readRand:  rand.Read,
	}
}

// Load returns the token in the file.
func (f *TokenFile) Load() (string, error) {
	b, err := f.readFile(f.Path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("%s is empty", f.Path)
	}
	return tok, nil
}

// LoadOrCreate returns the token in the file, creating the file with a new
// token when there's none.
func (f *TokenFile) LoadOrCreate() (string, error) {
	tok, err := f.Load()
	if err == nil {
		return tok, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return f.Rotate()
}

// Rotate writes a new token to the file and returns it. The dir is made
// private (0700) and the file is written 0600 and renamed into place, so a
// reader never sees it half-written.
func (f *TokenFile) Rotate() (string, error) {
	tok, err := newToken(f.readRand)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(f.Path)
	if err := f.mkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := f.chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("securing %s: %w", dir, err)
	}
	tmp := f.Path + ".tmp"
	if err := f.writeFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.rename(tmp, f.Path); err != nil {
		return "", fmt.Errorf("replacing %s: %w", f.Path, err)
	}
	return tok, nil
}

// newToken returns tokenBytes of randomness, hex encoded.
func newToken(readRand func([]byte) (int, error)) (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := readRand(b); err != nil {
		return "", fmt.Errorf("generating a token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
