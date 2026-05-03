package engine

import "os"

// OSFileSystem is the production FileSystem: a thin wrapper over os.ReadFile.
type OSFileSystem struct{}

// ReadFile reads the named file. Path resolution is the caller's job —
// the engine passes absolute paths.
func (OSFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
