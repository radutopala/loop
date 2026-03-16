package osutil

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// RealSystem delegates to real OS calls. Consumer packages define their own
// narrow interfaces that RealSystem satisfies via structural typing.
type RealSystem struct{}

func (RealSystem) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (RealSystem) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }
func (RealSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (RealSystem) ReadDir(name string) ([]fs.DirEntry, error)   { return os.ReadDir(name) }
func (RealSystem) Remove(name string) error                     { return os.Remove(name) }
func (RealSystem) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
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
