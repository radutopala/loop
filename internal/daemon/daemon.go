package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// System abstracts OS operations for testability.
type System interface {
	Executable() (string, error)
	UserHomeDir() (string, error)
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	RemoveFile(name string) error
	RunCommand(name string, args ...string) ([]byte, error)
	Stat(name string) (os.FileInfo, error)
	GetUID() int
	EvalSymlinks(path string) (string, error)
	Getenv(key string) string
}

// RealSystem implements System with real OS calls.
type RealSystem struct{}

func (RealSystem) Executable() (string, error)                  { return os.Executable() }
func (RealSystem) UserHomeDir() (string, error)                 { return os.UserHomeDir() }
func (RealSystem) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (RealSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (RealSystem) RemoveFile(name string) error { return os.Remove(name) }
func (RealSystem) RunCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
func (RealSystem) Stat(name string) (os.FileInfo, error)    { return os.Stat(name) }
func (RealSystem) GetUID() int                              { return os.Getuid() }
func (RealSystem) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }
func (RealSystem) Getenv(key string) string                 { return os.Getenv(key) }

// proxyKeys lists the environment variable names forwarded to the service unit.
var proxyKeys = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

func removeIfExists(sys System, path string) error {
	err := sys.RemoveFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing unit file: %w", err)
	}
	return nil
}
