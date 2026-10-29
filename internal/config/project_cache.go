package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"

	"github.com/radutopala/loop/internal/osutil"
)

// projectCache keeps each project config file a loader decoded, with what
// its stat said when read: a file whose stat is unchanged isn't read or
// decoded again. Loading changes what it decodes, so every caller gets its
// own deep copy.
type projectCache struct {
	// open opens a project config file without following a symlink out of
	// the project: the agent may write that dir.
	open  func(root, path string) (*os.File, error)
	mu    sync.Mutex
	files map[string]projectFile
}

// projectFile is a project config file's stat and content when last read,
// and what it decoded to.
type projectFile struct {
	stamp osutil.FileStamp
	data  []byte
	pc    *projectConfig
}

func newProjectCache() *projectCache {
	return &projectCache{open: osutil.OpenFileIn, files: map[string]projectFile{}}
}

// read decodes the project config file at path, under root. A missing one
// gives an error matching fs.ErrNotExist.
func (c *projectCache) read(root, path string) (*projectConfig, error) {
	f, err := c.open(root, path)
	if err != nil {
		return nil, fmt.Errorf("reading project config file: %w", err)
	}
	defer f.Close()
	// The stat is of the file read, before the read: a write during it
	// shows in the next one's.
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading project config file: %w", err)
	}
	stamp := osutil.StampOf(fi)
	c.mu.Lock()
	last, ok := c.files[path]
	c.mu.Unlock()
	if ok && last.stamp == stamp {
		return cloneProjectConfig(last.pc), nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading project config file: %w", err)
	}
	pc := last.pc
	if !ok || !bytes.Equal(data, last.data) {
		if pc, err = parseProjectConfig(data); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.files[path] = projectFile{stamp: stamp, data: data, pc: pc}
	c.mu.Unlock()
	return cloneProjectConfig(pc), nil
}

// cloneProjectConfig returns a copy of pc that shares nothing with it.
func cloneProjectConfig(pc *projectConfig) *projectConfig {
	out := new(projectConfig)
	deepCopy(reflect.ValueOf(out).Elem(), reflect.ValueOf(pc).Elem())
	return out
}

// deepCopy sets dst to a copy of src, copying down through pointers,
// slices, maps and interfaces so nothing changeable is shared. Unexported
// fields are copied as they are: projectConfig has none that refer to
// anything (a test checks).
func deepCopy(dst, src reflect.Value) {
	switch src.Kind() {
	case reflect.Pointer:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		p := reflect.New(src.Type().Elem())
		deepCopy(p.Elem(), src.Elem())
		dst.Set(p)
	case reflect.Interface:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		v := reflect.New(src.Elem().Type()).Elem()
		deepCopy(v, src.Elem())
		dst.Set(v)
	case reflect.Slice:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := range src.Len() {
			deepCopy(s.Index(i), src.Index(i))
		}
		dst.Set(s)
	case reflect.Map:
		if src.IsNil() {
			dst.SetZero()
			return
		}
		m := reflect.MakeMapWithSize(src.Type(), src.Len())
		for it := src.MapRange(); it.Next(); {
			v := reflect.New(src.Type().Elem()).Elem()
			deepCopy(v, it.Value())
			m.SetMapIndex(it.Key(), v)
		}
		dst.Set(m)
	case reflect.Struct:
		dst.Set(src)
		for i := range src.NumField() {
			if dst.Field(i).CanSet() {
				deepCopy(dst.Field(i), src.Field(i))
			}
		}
	case reflect.Array:
		for i := range src.Len() {
			deepCopy(dst.Index(i), src.Index(i))
		}
	default:
		dst.Set(src)
	}
}
