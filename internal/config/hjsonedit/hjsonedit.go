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
	op, err := appendOp(&v, path, item)
	if err != nil {
		return nil, nil, err
	}
	rootLines := multiline(&v)
	ops, err := json.Marshal([]any{op})
	if err == nil {
		err = v.Patch(ops)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("editing %s: %w", configPath, err)
	}
	indentAdded(&v, path, op, rootLines)
	if err := fsys.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", filepath.Dir(configPath), err)
	}
	after = v.Pack()
	if err := atomicWrite(fsys, configPath, after); err != nil {
		return nil, nil, err
	}
	return before, after, nil
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
	for i, key := range path {
		obj, ok := cur.Value.(*hujson.Object)
		if !ok {
			return patchOp{}, fmt.Errorf("%s is not an object", describe(path[:i]))
		}
		member := findMember(obj, key)
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

// indentAdded lays out what op added to v like the container it went into.
func indentAdded(v *hujson.Value, path []string, op patchOp, rootLines bool) {
	unit := indentUnit(v)
	switch {
	case strings.HasSuffix(op.Path, "/-"):
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
	before := string(h.BeforeExtra)
	if i := strings.LastIndexByte(before, '\n'); i >= 0 {
		before = before[:i]
	}
	h.BeforeExtra = hujson.Extra(before + "\n" + child)
	if len(children) == 1 {
		*closing = hujson.Extra("\n" + indent)
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
		before := string(obj.Members[i].Name.BeforeExtra)
		if j := strings.LastIndexByte(before, '\n'); j >= 0 {
			indent = before[j+1:]
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
			before := string(m.Name.BeforeExtra)
			if i := strings.LastIndexByte(before, '\n'); i >= 0 && i < len(before)-1 {
				return before[i+1:]
			}
		}
	}
	return "  "
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

// findMember returns the named member's value in obj, or nil.
func findMember(obj *hujson.Object, name string) *hujson.Value {
	if i := memberIndex(obj, name); i >= 0 {
		return &obj.Members[i].Value
	}
	return nil
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
