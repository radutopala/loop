package osutil

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RealSystem delegates to real OS calls. Consumer packages define their own
// narrow interfaces that RealSystem satisfies via structural typing.
type RealSystem struct{}

// safePath cleans `name` and rejects any result that still contains a ".."
// traversal segment. Returning the cleaned path keeps the compiler's taint
// analysis (and CodeQL) happy: callers use this single validated value in
// their subsequent OS call.
func safePath(name string) (string, error) {
	clean := filepath.Clean(name)
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("path contains disallowed traversal: %s", clean)
	}
	return clean, nil
}

func (RealSystem) Stat(name string) (os.FileInfo, error) {
	clean, err := safePath(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(clean)
}
func (RealSystem) ReadFile(name string) ([]byte, error) {
	clean, err := safePath(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(clean)
}
func (RealSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	clean, err := safePath(name)
	if err != nil {
		return err
	}
	return os.WriteFile(clean, data, perm)
}
func (RealSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	clean, err := safePath(name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(clean)
}
func (RealSystem) Remove(name string) error {
	clean, err := safePath(name)
	if err != nil {
		return err
	}
	return os.Remove(clean)
}
func (RealSystem) RemoveAll(path string) error {
	clean, err := safePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(clean)
}

func (RealSystem) Open(name string) (*os.File, error) {
	clean, err := safePath(name)
	if err != nil {
		return nil, err
	}
	return os.Open(clean)
}
func (RealSystem) MkdirAll(path string, perm os.FileMode) error {
	clean, err := safePath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(clean, perm)
}
func (RealSystem) Readlink(name string) (string, error)         { return os.Readlink(name) }
func (RealSystem) UserHomeDir() (string, error)                 { return os.UserHomeDir() }
func (RealSystem) Getenv(key string) string                     { return os.Getenv(key) }
func (RealSystem) EvalSymlinks(path string) (string, error)     { return filepath.EvalSymlinks(path) }
func (RealSystem) WalkDir(root string, fn fs.WalkDirFunc) error { return filepath.WalkDir(root, fn) }
func (RealSystem) ExecCommandOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}
func (RealSystem) Getwd() (string, error)                    { return os.Getwd() }
func (RealSystem) Executable() (string, error)               { return os.Executable() }
func (RealSystem) Chmod(name string, mode os.FileMode) error { return os.Chmod(name, mode) }
func (RealSystem) Rename(oldpath, newpath string) error      { return os.Rename(oldpath, newpath) }
func (RealSystem) CreateTemp(dir, pattern string) (*os.File, error) {
	return os.CreateTemp(dir, pattern)
}

// EncodeClaudeProjectPath encodes a directory path the same way Claude Code does:
// replace "/" and "." with "-".
func EncodeClaudeProjectPath(dirPath string) string {
	r := strings.NewReplacer("/", "-", ".", "-")
	return r.Replace(dirPath)
}
