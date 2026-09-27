// Package hjsonedit makes small edits to an HJSON config file in place: the
// user's comments, key order and formatting survive, unlike a round trip
// through encoding/json.
package hjsonedit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// FS is the filesystem the edits go through.
type FS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Remove(name string) error
	MkdirAll(path string, perm os.FileMode) error
}

// Append adds item to the end of the array at path (object keys from the
// top level down) in the config file at configPath. A missing file, missing
// objects along path and a missing array are all created; a new or empty
// array starts with seed's elements, then item. The write is atomic.
func Append(fsys FS, configPath string, path []string, item any, seed []any) error {
	if len(path) == 0 {
		return errors.New("empty path")
	}
	data, err := fsys.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading %s: %w", configPath, err)
		}
		data = []byte("{}\n")
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", configPath, err)
	}
	op, err := appendOp(&v, path, item, seed)
	if err != nil {
		return err
	}
	ops, _ := json.Marshal([]any{op})
	if err := v.Patch(ops); err != nil {
		return fmt.Errorf("editing %s: %w", configPath, err)
	}
	if err := fsys.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(configPath), err)
	}
	return atomicWrite(fsys, configPath, v.Pack())
}

// patchOp is one RFC 6902 operation.
type patchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// appendOp returns the patch that appends item at path in v: "add" at the
// array's end when it exists, else "add" of the first missing key with the
// rest of path built around the new array.
func appendOp(v *hujson.Value, path []string, item any, seed []any) (patchOp, error) {
	cur := v
	for i, key := range path {
		obj, ok := cur.Value.(*hujson.Object)
		if !ok {
			return patchOp{}, fmt.Errorf("%s is not an object", describe(path[:i]))
		}
		member := findMember(obj, key)
		if member == nil {
			var value any = append(append([]any{}, seed...), item)
			for j := len(path) - 1; j > i; j-- {
				value = map[string]any{path[j]: value}
			}
			return patchOp{Op: "add", Path: pointer(path[:i+1]), Value: value}, nil
		}
		cur = member
	}
	arr, ok := cur.Value.(*hujson.Array)
	if !ok {
		return patchOp{}, fmt.Errorf("%s is not an array", describe(path))
	}
	if len(arr.Elements) == 0 && len(seed) > 0 {
		return patchOp{Op: "replace", Path: pointer(path), Value: append(append([]any{}, seed...), item)}, nil
	}
	return patchOp{Op: "add", Path: pointer(path) + "/-", Value: item}, nil
}

// findMember returns the named member's value in obj, or nil.
func findMember(obj *hujson.Object, name string) *hujson.Value {
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && lit.String() == name {
			return &obj.Members[i].Value
		}
	}
	return nil
}

// pointer is the RFC 6901 JSON pointer for path.
func pointer(path []string) string {
	var b strings.Builder
	for _, key := range path {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(key))
	}
	return b.String()
}

// describe names path for an error message.
func describe(path []string) string {
	if len(path) == 0 {
		return "the top level"
	}
	return strings.Join(path, ".")
}

// atomicWrite writes data to a temp file beside path and renames it into
// place, so a crash mid-write never leaves a truncated config.
func atomicWrite(fsys FS, path string, data []byte) error {
	tmp := path + ".tmp"
	if err := fsys.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := fsys.Rename(tmp, path); err != nil {
		_ = fsys.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
