package testutil

import (
	"io/fs"
	"os"

	"github.com/stretchr/testify/mock"
)

// MockSystem provides mock OS operations for testing. It satisfies all
// per-package system interfaces (clientSystem, runnerSystem, serverSystem,
// indexerSystem) via structural typing.
type MockSystem struct {
	mock.Mock
}

func (m *MockSystem) Stat(name string) (os.FileInfo, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(os.FileInfo), args.Error(1)
}

func (m *MockSystem) ReadFile(name string) ([]byte, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return m.Called(name, data, perm).Error(0)
}

func (m *MockSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]fs.DirEntry), args.Error(1)
}

func (m *MockSystem) Remove(name string) error {
	return m.Called(name).Error(0)
}

func (m *MockSystem) MkdirAll(path string, perm os.FileMode) error {
	return m.Called(path, perm).Error(0)
}

func (m *MockSystem) Readlink(name string) (string, error) {
	args := m.Called(name)
	return args.String(0), args.Error(1)
}

func (m *MockSystem) UserHomeDir() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockSystem) Getenv(key string) string {
	return m.Called(key).String(0)
}

func (m *MockSystem) EvalSymlinks(path string) (string, error) {
	args := m.Called(path)
	return args.String(0), args.Error(1)
}

func (m *MockSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	args := m.Called(root, fn)
	return args.Error(0)
}

func (m *MockSystem) ExecCommandOutput(name string, args ...string) ([]byte, error) {
	callArgs := m.Called(name, args)
	if callArgs.Get(0) == nil {
		return nil, callArgs.Error(1)
	}
	return callArgs.Get(0).([]byte), callArgs.Error(1)
}

func (m *MockSystem) Getwd() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockSystem) Executable() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockSystem) Chmod(name string, mode os.FileMode) error {
	return m.Called(name, mode).Error(0)
}

func (m *MockSystem) Rename(oldpath, newpath string) error {
	return m.Called(oldpath, newpath).Error(0)
}

func (m *MockSystem) CreateTemp(dir, pattern string) (*os.File, error) {
	args := m.Called(dir, pattern)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*os.File), args.Error(1)
}

// Override removes all existing expectations for the given method and returns
// a new Call that can be configured with .Return(). This is useful for
// overriding defaults set in SetupTest.
func (m *MockSystem) Override(method string, args ...any) *mock.Call {
	for i := len(m.ExpectedCalls) - 1; i >= 0; i-- {
		if m.ExpectedCalls[i].Method == method {
			m.ExpectedCalls[i].Unset()
		}
	}
	return m.On(method, args...)
}
