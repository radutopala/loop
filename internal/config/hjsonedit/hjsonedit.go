// Package hjsonedit makes small edits to an HJSON config file in place: the
// user's comments, key order and formatting survive, unlike a round trip
// through encoding/json.
//
// hjsonedit_spec_test.go states and checks what an edit keeps and changes.
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
// objects along path and a missing array are all created. What's added is laid out one
// value per line with the file's indentation, unless it lands in a container
// written on one line. The write is atomic.
func Append(fsys FS, configPath string, path []string, item any) error {
	_, _, err := AppendData(fsys, configPath, path, item)
	return err
}

// AppendData is Append, and returns the file content it read (nil for a
// missing file) and the content it wrote.
func AppendData(fsys FS, configPath string, path []string, item any) (before, after []byte, err error) {
	before, after, err = Appended(fsys, configPath, path, item)
	if err != nil {
		return nil, nil, err
	}
	return write(fsys, configPath, before, after)
}

// Appended is AppendData without the write: the file content it reads (nil
// for a missing file) and the content Append would write, for showing the
// edit before making it.
func Appended(fsys FS, configPath string, path []string, item any) (before, after []byte, err error) {
	return edited(fsys, configPath, path, func(v *hujson.Value) (patchOp, error) { return appendOp(v, path, item) })
}

// SetData sets the value at path (object keys from the top level down) in
// the config file at configPath to value, replacing what's there. A missing
// file and missing objects along path are created. It returns the file
// content it read (nil for a missing file) and the content it wrote. The
// write is atomic.
func SetData(fsys FS, configPath string, path []string, value any) (before, after []byte, err error) {
	before, after, err = edited(fsys, configPath, path, func(v *hujson.Value) (patchOp, error) { return setOp(v, path, value) })
	if err != nil {
		return nil, nil, err
	}
	return write(fsys, configPath, before, after)
}

// write writes after to configPath, creating its dir.
func write(fsys FS, configPath string, before, after []byte) ([]byte, []byte, error) {
	if err := fsys.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", filepath.Dir(configPath), err)
	}
	if err := atomicWrite(fsys, configPath, after); err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// edited returns the content of the config file at configPath (nil for a
// missing file) and that content with the patch op makes applied.
func edited(fsys FS, configPath string, path []string, op func(*hujson.Value) (patchOp, error)) (before, after []byte, err error) {
	if len(path) == 0 {
		return nil, nil, errors.New("empty path")
	}
	before, err = fsys.ReadFile(configPath)
	data := before
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("reading %s: %w", configPath, err)
		}
		before, data = nil, []byte("{}\n")
	}
	v, err := hujson.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}
	patch, err := op(&v)
	if err != nil {
		return nil, nil, err
	}
	rootLines := multiline(&v)
	ops, err := json.Marshal([]any{patch})
	if err == nil {
		err = v.Patch(ops)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("editing %s: %w", configPath, err)
	}
	if patch.Op == "add" {
		indentAdded(&v, path, patch, rootLines)
	}
	return before, v.Pack(), nil
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
func appendOp(v *hujson.Value, path []string, item any) (patchOp, error) {
	cur := v
	for i := range path {
		member, err := pathMember(cur, path, i)
		if err != nil {
			return patchOp{}, err
		}
		if member == nil {
			var value any = []any{item}
			for j := len(path) - 1; j > i; j-- {
				value = map[string]any{path[j]: value}
			}
			return patchOp{Op: "add", Path: pointer(path[:i+1]), Value: value}, nil
		}
		cur = member
	}
	if _, ok := cur.Value.(*hujson.Array); !ok {
		return patchOp{}, fmt.Errorf("%s is not an array", describe(path))
	}
	return patchOp{Op: "add", Path: pointer(path) + "/-", Value: item}, nil
}

// setOp returns the patch that sets path in v to value: "replace" when it
// exists, else "add" of the first missing key with the rest of path built
// around value.
func setOp(v *hujson.Value, path []string, value any) (patchOp, error) {
	cur := v
	for i := range path {
		member, err := pathMember(cur, path, i)
		if err != nil {
			return patchOp{}, err
		}
		if member == nil {
			for j := len(path) - 1; j > i; j-- {
				value = map[string]any{path[j]: value}
			}
			return patchOp{Op: "add", Path: pointer(path[:i+1]), Value: value}, nil
		}
		cur = member
	}
	return patchOp{Op: "replace", Path: pointer(path), Value: value}, nil
}

// indentAdded lays out what op added to v like the container it went into.
func indentAdded(v *hujson.Value, path []string, op patchOp, rootLines bool) {
	unit := indentUnit(v)
	switch {
	case op.Path == pointer(path)+"/-":
		arr := v.Find(pointer(path)).Value.(*hujson.Array)
		placeLast(arr.Elements, &arr.AfterExtra, lineIndent(v, path), unit, rootLines, func(e *hujson.Value) *hujson.Value { return e })
	default:
		key := strings.Count(op.Path, "/") - 1
		obj := v.Find(pointer(path[:key])).Value.(*hujson.Object)
		placeLast(obj.Members, &obj.AfterExtra, lineIndent(v, path[:key]), unit, rootLines, func(m *hujson.ObjectMember) *hujson.Value { return &m.Name })
	}
}

// placeLast puts the last child on its own line in a one-per-line container.
func placeLast[T any](children []T, closing *hujson.Extra, indent, unit string, rootLines bool, head func(*T) *hujson.Value) {
	last := &children[len(children)-1]
	h := head(last)
	lines := len(children) == 1 && rootLines
	for i := range children[:len(children)-1] {
		if strings.Contains(string(head(&children[i]).BeforeExtra), "\n") {
			lines = true
		}
	}
	if !lines {
		return
	}
	child := indent + unit
	// Drop the child's own line break and indentation, but no comment
	// (a block comment can hold a newline).
	before := strings.TrimSuffix(strings.TrimRight(string(h.BeforeExtra), " \t"), "\n")
	h.BeforeExtra = hujson.Extra(before + "\n" + child)
	if len(children) == 1 {
		// The container was empty: keep any comment it held.
		kept := strings.TrimSpace(string(*closing))
		if kept != "" {
			kept = " " + kept
		}
		*closing = hujson.Extra(kept + "\n" + indent)
	}
	value := h
	if m, ok := any(last).(*hujson.ObjectMember); ok {
		m.Value.BeforeExtra = hujson.Extra(" ")
		value = &m.Value
	}
	layout(value, child, unit)
}

// layout puts each value inside v on its own line, indented one unit per
// level below indent, the line v starts on. It's for values Append just
// made, which carry no comments to keep.
func layout(v *hujson.Value, indent, unit string) {
	switch c := v.Value.(type) {
	case *hujson.Object:
		if len(c.Members) == 0 {
			return
		}
		for i := range c.Members {
			m := &c.Members[i]
			m.Name.BeforeExtra, m.Name.AfterExtra = hujson.Extra("\n"+indent+unit), nil
			m.Value.BeforeExtra, m.Value.AfterExtra = hujson.Extra(" "), nil
			layout(&m.Value, indent+unit, unit)
		}
		c.AfterExtra = hujson.Extra("\n" + indent)
	case *hujson.Array:
		if len(c.Elements) == 0 {
			return
		}
		for i := range c.Elements {
			e := &c.Elements[i]
			e.BeforeExtra, e.AfterExtra = hujson.Extra("\n"+indent+unit), nil
			layout(e, indent+unit, unit)
		}
		c.AfterExtra = hujson.Extra("\n" + indent)
	}
}

// multiline reports whether v, an object, is written one member per line,
// or is empty (which Append fills that way).
func multiline(v *hujson.Value) bool {
	obj := v.Value.(*hujson.Object)
	if len(obj.Members) == 0 || strings.Contains(string(obj.AfterExtra), "\n") {
		return true
	}
	for _, m := range obj.Members {
		if strings.Contains(string(m.Name.BeforeExtra), "\n") {
			return true
		}
	}
	return false
}

// lineIndent returns the indentation of the line the value at path starts
// on: the whitespace after the last newline before it (its member name's, in
// an object), or its parent's when it shares the parent's line.
func lineIndent(v *hujson.Value, path []string) string {
	indent := ""
	cur := v
	for _, key := range path {
		obj := cur.Value.(*hujson.Object)
		i := memberIndex(obj, key)
		if ws, ok := lastLineIndent(obj.Members[i].Name.BeforeExtra); ok {
			indent = ws
		}
		cur = &obj.Members[i].Value
	}
	return indent
}

// indentUnit is the file's indentation step: the indentation of the first
// top-level member on its own line, else two spaces.
func indentUnit(v *hujson.Value) string {
	if obj, ok := v.Value.(*hujson.Object); ok {
		for _, m := range obj.Members {
			if ws, _ := lastLineIndent(m.Name.BeforeExtra); ws != "" {
				return ws
			}
		}
	}
	return "  "
}

// lastLineIndent returns the whitespace that starts the last line of e (a
// comment there isn't indentation), and whether e has a newline at all.
func lastLineIndent(e hujson.Extra) (string, bool) {
	s := string(e)
	i := strings.LastIndexByte(s, '\n')
	if i < 0 {
		return "", false
	}
	line := s[i+1:]
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))], true
}

// memberIndex returns the index of the named member in obj, or -1.
func memberIndex(obj *hujson.Object, name string) int {
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && lit.String() == name {
			return i
		}
	}
	return -1
}

// pathMember returns the value of path[i] in cur, the value at path[:i], or
// nil when it has none. cur must be an object, and path[i] in it once: an
// edit goes to the first of two members with one name, but a config load
// reads the last.
func pathMember(cur *hujson.Value, path []string, i int) (*hujson.Value, error) {
	obj, ok := cur.Value.(*hujson.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not an object", describe(path[:i]))
	}
	j := memberIndex(obj, path[i])
	if j < 0 {
		return nil, nil
	}
	if memberIndex(&hujson.Object{Members: obj.Members[j+1:]}, path[i]) >= 0 {
		return nil, fmt.Errorf("%s is set more than once", describe(path[:i+1]))
	}
	return &obj.Members[j].Value, nil
}

// pointer is the RFC 6901 JSON pointer for path ("" for the top level). The
// keys are fixed config identifiers, so none needs escaping.
func pointer(path []string) string {
	return strings.Join(append([]string{""}, path...), "/")
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
