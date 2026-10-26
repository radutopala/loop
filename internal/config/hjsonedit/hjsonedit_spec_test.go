// hjsonedit_spec_test.go checks the package's edits as a spec.
//
// The property, for a config text doc (nil for a missing file, read as {}),
// a path of object keys and a JSON value item:
//
//   - Data: an edit succeeds exactly when the same edit of parse(doc), the
//     plain JSON data, does, and then parse(result) is that edit's result.
//     Append adds item to the end of the array at path, creating the missing
//     objects along path and the array as [item]; Set puts item at path,
//     replacing what's there and creating the missing objects along path.
//     Both fail for an empty path, a non-object on the way, (Append) a
//     non-array at path, and a key along path that doc sets more than once
//     in its object, which the data can't show: the edit would go to the
//     first, the data holds the last. Format is the identity on the data.
//   - Comments: every comment of doc is in the result, in the same order,
//     and no other, except the ones inside a value Set replaces.
//   - Untouched text: Append and Set leave the text of every value off path
//     (and, for Append, of the elements already in the array) byte for byte
//     as it was.
//   - Writes: an edit that writes reports the content it read and the content
//     it wrote, which is then the file; one that fails leaves the file as it
//     was.
//
// Precondition: path keys are valid UTF-8 and hold no "/" or "~" (pointer
// doesn't escape them; callers pass fixed config identifiers).
//
// The trusted base: hujson.Parse and hujson.Standardize followed by
// encoding/json (into any, with UseNumber) as the reference parser, the same
// pair the config loader uses, so a key set twice means its last value. The
// comment walk below trusts hujson's tree to hold every comment in some
// Extra. Generated documents come from genDoc and the committed fuzz seeds
// in testdata/fuzz/FuzzEdit.
package hjsonedit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/tailscale/hujson"
)

// specPath is where the edited config lives in memFS.
const specPath = "/proj/.loop/config.json"

// memFS is an in-memory FS.
type memFS struct{ files map[string][]byte }

func (m memFS) ReadFile(name string) ([]byte, error) {
	b, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return bytes.Clone(b), nil
}

func (m memFS) WriteFile(name string, data []byte, _ os.FileMode) error {
	m.files[name] = bytes.Clone(data)
	return nil
}

func (m memFS) Rename(oldpath, newpath string) error {
	m.files[newpath] = m.files[oldpath]
	delete(m.files, oldpath)
	return nil
}

func (m memFS) Remove(name string) error {
	delete(m.files, name)
	return nil
}

func (memFS) MkdirAll(string, os.FileMode) error { return nil }

// opKind is the edit an operation makes, as the spec models it.
type opKind int

const (
	kindAppend opKind = iota
	kindSet
	kindFormat
)

// specOp is one exported operation: its kind and how to run it against a
// filesystem. run returns the content the op read and the content it made
// (for an op that writes, what's in the file after it).
type specOp struct {
	kind  opKind
	write bool
	run   func(fsys FS, path []string, item any) (before, after []byte, err error)
}

// specOps classifies every exported func of the package; TestOpClassification
// fails until a new one is added here.
func specOps() map[string]specOp {
	return map[string]specOp{
		"Append": {kind: kindAppend, write: true, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			before, _ := fsys.ReadFile(specPath)
			if err := Append(fsys, specPath, path, item); err != nil {
				return nil, nil, err
			}
			after, err := fsys.ReadFile(specPath)
			return before, after, err
		}},
		"AppendData": {kind: kindAppend, write: true, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			return AppendData(fsys, specPath, path, item)
		}},
		"Appended": {kind: kindAppend, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			return Appended(fsys, specPath, path, item)
		}},
		"SetData": {kind: kindSet, write: true, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			return SetData(fsys, specPath, path, item)
		}},
		"Format": {kind: kindFormat, run: func(fsys FS, _ []string, _ any) ([]byte, []byte, error) {
			before, err := fsys.ReadFile(specPath)
			if err != nil {
				return nil, nil, err
			}
			after, err := Format(before)
			return before, after, err
		}},
	}
}

// editCase is one input: doc is the file (nil when missing).
type editCase struct {
	doc  []byte
	path []string
	item any
}

func (c editCase) String() string {
	item, _ := json.Marshal(c.item)
	if c.doc == nil {
		return fmt.Sprintf("missing file, path %q, item %s", c.path, item)
	}
	return fmt.Sprintf("doc %q, path %q, item %s", c.doc, c.path, item)
}

// parseData is the reference parser: hujson to standard JSON, then
// encoding/json.
func parseData(data []byte) (any, error) {
	std, err := hujson.Standardize(bytes.Clone(data))
	if err != nil {
		return nil, err
	}
	return decodeJSON(std)
}

func decodeJSON(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// modelEdit applies the edit of kind to data, plain JSON.
func modelEdit(kind opKind, data any, path []string, item any) (any, error) {
	if kind == kindFormat {
		return data, nil
	}
	if len(path) == 0 {
		return nil, errors.New("empty path")
	}
	obj, ok := data.(map[string]any)
	if !ok {
		return nil, errors.New("not an object")
	}
	key := path[0]
	child, ok := obj[key]
	switch {
	case !ok:
		child = item
		if kind == kindAppend {
			child = []any{item}
		}
		for j := len(path) - 1; j > 0; j-- {
			child = map[string]any{path[j]: child}
		}
	case len(path) > 1:
		var err error
		if child, err = modelEdit(kind, child, path[1:], item); err != nil {
			return nil, err
		}
	case kind == kindSet:
		child = item
	default:
		arr, ok := child.([]any)
		if !ok {
			return nil, errors.New("not an array")
		}
		child = append(slices.Clone(arr), item)
	}
	out := make(map[string]any, len(obj)+1)
	for k, v := range obj {
		out[k] = v
	}
	out[key] = child
	return out, nil
}

// splitComments returns the comments in e, each without the newline that
// ends a // one.
func splitComments(e hujson.Extra, out []string) []string {
	s := string(e)
	for i := 0; i < len(s); {
		if s[i] != '/' {
			i++
			continue
		}
		end := commentEnd(s, i)
		out = append(out, s[i:end])
		i = end
	}
	return out
}

// comments returns v's comments in document order, leaving out the ones
// inside skip (but keeping skip's own extras).
func comments(v, skip *hujson.Value, out []string) []string {
	out = splitComments(v.BeforeExtra, out)
	if v != skip {
		switch c := v.Value.(type) {
		case *hujson.Object:
			for i := range c.Members {
				out = comments(&c.Members[i].Name, skip, out)
				out = comments(&c.Members[i].Value, skip, out)
			}
			out = splitComments(c.AfterExtra, out)
		case *hujson.Array:
			for i := range c.Elements {
				out = comments(&c.Elements[i], skip, out)
			}
			out = splitComments(c.AfterExtra, out)
		}
	}
	return splitComments(v.AfterExtra, out)
}

// inner is v's text without its own leading and trailing extras.
func inner(v hujson.Value) string {
	v.BeforeExtra, v.AfterExtra = nil, nil
	return string(v.Pack())
}

// lookup follows path in v by first matching name, as hujson.Find does,
// and reports whether a name along the way is in its object more than once.
func lookup(v *hujson.Value, path []string) (*hujson.Value, bool) {
	for _, key := range path {
		obj, ok := v.Value.(*hujson.Object)
		if !ok {
			return nil, false
		}
		v = nil
		for i := range obj.Members {
			if obj.Members[i].Name.Value.(hujson.Literal).String() != key {
				continue
			}
			if v != nil {
				return nil, true
			}
			v = &obj.Members[i].Value
		}
		if v == nil {
			return nil, false
		}
	}
	return v, false
}

// untouched checks that every value of before off path (and every element
// of the array at path, for Append) has the same text in after, walking
// both trees in step: an edit only adds at a container's end or replaces in
// place, so the i-th child of a container on path is the i-th in after.
func untouched(before, after *hujson.Value, path []string, kind opKind) error {
	switch b := before.Value.(type) {
	case *hujson.Object:
		if len(path) == 0 {
			break
		}
		a, ok := after.Value.(*hujson.Object)
		if !ok || len(a.Members) < len(b.Members) {
			return fmt.Errorf("object on path at %q lost members", path)
		}
		for i := range b.Members {
			bm, am := &b.Members[i], &a.Members[i]
			if inner(bm.Name) != inner(am.Name) {
				return fmt.Errorf("member %d renamed from %s to %s", i, inner(bm.Name), inner(am.Name))
			}
			if bm.Name.Value.(hujson.Literal).String() == path[0] {
				if len(path) == 1 && kind == kindSet {
					continue
				}
				if err := untouched(&bm.Value, &am.Value, path[1:], kind); err != nil {
					return err
				}
				continue
			}
			if inner(bm.Value) != inner(am.Value) {
				return fmt.Errorf("untouched member %s changed from %q to %q", inner(bm.Name), inner(bm.Value), inner(am.Value))
			}
		}
		return nil
	case *hujson.Array:
		if len(path) > 0 || kind != kindAppend {
			break
		}
		a, ok := after.Value.(*hujson.Array)
		if !ok || len(a.Elements) != len(b.Elements)+1 {
			return errors.New("appended array lost its elements")
		}
		for i := range b.Elements {
			if inner(b.Elements[i]) != inner(a.Elements[i]) {
				return fmt.Errorf("element %d changed from %q to %q", i, inner(b.Elements[i]), inner(a.Elements[i]))
			}
		}
		return nil
	}
	return nil
}

// checkEdit checks the property for op on c. It reports whether the case
// was in the property's domain, and the violation it found.
func checkEdit(op specOp, c editCase) (bool, error) {
	for _, key := range c.path {
		if strings.ContainsAny(key, "/~") || !utf8.ValidString(key) {
			return false, nil
		}
	}
	if op.kind == kindFormat && c.doc == nil {
		return false, nil
	}
	fsys := memFS{files: map[string][]byte{}}
	if c.doc != nil {
		fsys.files[specPath] = bytes.Clone(c.doc)
	}
	itemJSON, err := json.Marshal(c.item)
	if err != nil {
		return false, nil
	}
	item, _ := decodeJSON(itemJSON)

	before, after, err := op.run(fsys, c.path, c.item)

	doc := c.doc
	if doc == nil {
		doc = []byte("{}")
	}
	data, perr := parseData(doc)
	var want any
	if perr == nil {
		want, perr = modelEdit(op.kind, data, c.path, item)
	}
	bv, _ := hujson.Parse(doc) // parses: parseData did
	replaced, dup := lookup(&bv, c.path)
	if perr == nil && dup && op.kind != kindFormat {
		perr = errors.New("a key along path is set more than once")
	}
	if file, ok := fsys.files[specPath]; (ok != (c.doc != nil) || !bytes.Equal(file, c.doc)) && (err != nil || !op.write) {
		return true, fmt.Errorf("file changed to %q though the op failed or doesn't write (err %v)", file, err)
	}
	if (err != nil) != (perr != nil) {
		return true, fmt.Errorf("op error %v, model error %v", err, perr)
	}
	if err != nil {
		return true, nil
	}
	if !bytes.Equal(before, c.doc) || (before == nil) != (c.doc == nil) {
		return true, fmt.Errorf("op read %q", before)
	}
	if file := fsys.files[specPath]; op.write && !bytes.Equal(file, after) {
		return true, fmt.Errorf("op reports %q but wrote %q", after, file)
	}
	got, err := parseData(after)
	if err != nil {
		return true, fmt.Errorf("result %q doesn't parse: %w", after, err)
	}
	if !reflect.DeepEqual(want, got) {
		return true, fmt.Errorf("result %q is %v, want %v", after, got, want)
	}

	av, err := hujson.Parse(after)
	if err != nil {
		return true, err
	}
	var skip *hujson.Value
	if op.kind == kindSet {
		skip = replaced
	}
	if wantC, gotC := comments(&bv, skip, nil), comments(&av, nil, nil); !slices.Equal(wantC, gotC) {
		return true, fmt.Errorf("result %q has comments %q, want %q", after, gotC, wantC)
	}
	if op.kind != kindFormat {
		if err := untouched(&bv, &av, c.path, op.kind); err != nil {
			return true, fmt.Errorf("result %q: %w", after, err)
		}
	}
	return true, nil
}

// specKeys are the keys documents and paths are made of: few, so paths hit
// what's there and objects repeat keys.
var specKeys = []string{"a", "b", "mounts"}

// docGen writes a random config text: every container on one line or one
// member per line, every gap between tokens empty, spaced or holding a
// uniquely numbered comment, and a trailing comma now and then.
type docGen struct {
	r *rand.Rand
	b strings.Builder
	n int
}

// extra writes a random gap at indent.
func (g *docGen) extra(indent string) {
	switch g.r.IntN(9) {
	case 0, 1, 2:
	case 3:
		g.b.WriteString(" ")
	case 4:
		g.b.WriteString("\n" + indent)
	case 5:
		g.n++
		fmt.Fprintf(&g.b, " /* c%d */ ", g.n)
	case 6:
		g.n++
		fmt.Fprintf(&g.b, " // c%d\n%s", g.n, indent)
	case 7:
		g.n++
		fmt.Fprintf(&g.b, "\n\n%s// c%d\n%s", indent, g.n, indent)
	default:
		g.n++
		fmt.Fprintf(&g.b, "/*c%d*/", g.n)
	}
}

// value writes a random value at depth, on a line indented by indent.
func (g *docGen) value(depth int, indent string) {
	kind := g.r.IntN(4)
	if depth >= 3 {
		kind = 3
	}
	switch kind {
	case 0, 1:
		g.container('{', '}', depth, indent, func(child string) {
			g.b.WriteString(strconv.Quote(specKeys[g.r.IntN(len(specKeys))]))
			g.extra(child)
			g.b.WriteString(":")
			g.extra(child)
			g.value(depth+1, child)
		})
	case 2:
		g.container('[', ']', depth, indent, func(child string) { g.value(depth+1, child) })
	default:
		g.b.WriteString([]string{`1`, `-2.5`, `"s"`, `true`, `null`, `"x y"`}[g.r.IntN(6)])
	}
}

// container writes a random object or array whose members member writes.
func (g *docGen) container(open, closing byte, depth int, indent string, member func(child string)) {
	lines := g.r.IntN(2) == 0
	child := indent + "  "
	g.b.WriteByte(open)
	n := g.r.IntN(4)
	for i := range n {
		if lines {
			g.b.WriteString("\n" + child)
		}
		g.extra(child)
		member(child)
		g.extra(child)
		if i < n-1 || g.r.IntN(3) == 0 {
			g.b.WriteString(",")
			if !lines && i < n-1 {
				g.b.WriteString(" ")
			}
		}
	}
	g.extra(child)
	if lines {
		g.b.WriteString("\n" + indent)
	}
	g.b.WriteByte(closing)
}

// genCase returns a random case: mostly an object document, now and then a
// missing file or another top-level value.
func genCase(r *rand.Rand) editCase {
	g := &docGen{r: r}
	var doc []byte
	if r.IntN(12) > 0 {
		g.extra("")
		if r.IntN(10) > 0 {
			g.container('{', '}', 0, "", func(child string) {
				g.b.WriteString(strconv.Quote(specKeys[r.IntN(len(specKeys))]))
				g.extra(child)
				g.b.WriteString(":")
				g.extra(child)
				g.value(1, child)
			})
		} else {
			g.value(1, "")
		}
		g.extra("")
		g.b.WriteString("\n")
		doc = []byte(g.b.String())
	}
	n := 1 + r.IntN(3)
	if r.IntN(20) == 0 {
		n = 0
	}
	path := make([]string, n)
	for i := range path {
		path[i] = specKeys[r.IntN(len(specKeys))]
	}
	items := []any{1, "/new/path", true, nil, []any{"x", 2}, map[string]any{"k": []any{}}, map[string]any{}}
	return editCase{doc: doc, path: path, item: items[r.IntN(len(items))]}
}

// findCounterexample runs op on n generated cases and returns the first
// violation of the property, and how many cases it checked.
func findCounterexample(op specOp, seed uint64, n int) (int, error) {
	r := rand.New(rand.NewPCG(seed, seed^0x5eed))
	checked := 0
	for range n {
		c := genCase(r)
		ok, err := checkEdit(op, c)
		if err != nil {
			return checked, fmt.Errorf("%s: %w", c, err)
		}
		if ok {
			checked++
		}
	}
	return checked, nil
}

// FuzzEdit checks the property for raw config texts: path is dot-separated
// keys and item JSON text. Seeds are in testdata/fuzz/FuzzEdit.
func FuzzEdit(f *testing.F) {
	ops := specOps()
	f.Fuzz(func(t *testing.T, op string, doc string, missing bool, path string, item string) {
		o, ok := ops[op]
		if !ok {
			return
		}
		var v any
		if json.Unmarshal([]byte(item), &v) != nil {
			return
		}
		c := editCase{item: v}
		if !missing {
			c.doc = []byte(doc)
		}
		if path != "" {
			c.path = strings.Split(path, ".")
		}
		_, err := checkEdit(o, c)
		require.NoError(t, err, "%s %s", op, c)
	})
}

type EditSpecSuite struct {
	suite.Suite
}

func TestEditSpecSuite(t *testing.T) {
	suite.Run(t, new(EditSpecSuite))
}

// TestGenerated checks every operation on generated cases.
func (s *EditSpecSuite) TestGenerated() {
	for name, op := range specOps() {
		s.Run(name, func() {
			checked, err := findCounterexample(op, 1, 3000)
			require.NoError(s.T(), err)
			require.Greater(s.T(), checked, 2000)
		})
	}
}

// TestBrokenOpsFail: the checker finds a counterexample for an op that gets
// the data, the comments, the untouched text or the file wrong.
func (s *EditSpecSuite) TestBrokenOpsFail() {
	ops := specOps()
	tests := []struct {
		name string
		op   specOp
	}{
		{"set appends", specOp{kind: kindSet, run: ops["Appended"].run}},
		{"append twice", specOp{kind: kindAppend, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			before, after, err := Appended(fsys, specPath, path, item)
			if err != nil {
				return nil, nil, err
			}
			_, after, err = Appended(memFS{files: map[string][]byte{specPath: after}}, specPath, path, item)
			return before, after, err
		}}},
		{"drops comments", specOp{kind: kindAppend, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			before, after, err := Appended(fsys, specPath, path, item)
			if err != nil {
				return nil, nil, err
			}
			std, err := hujson.Standardize(after)
			return before, std, err
		}}},
		{"reformats", specOp{kind: kindSet, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			before, after, err := ops["SetData"].run(fsys, path, item)
			if err != nil {
				return nil, nil, err
			}
			after, err = Format(after)
			return before, after, err
		}}},
		{"writes on preview", specOp{kind: kindSet, run: ops["SetData"].run}},
		{"writes on failure", specOp{kind: kindSet, write: true, run: func(fsys FS, path []string, item any) ([]byte, []byte, error) {
			before, after, err := SetData(fsys, specPath, path, item)
			if err != nil {
				_ = fsys.WriteFile(specPath, []byte("{}"), 0o644)
			}
			return before, after, err
		}}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := findCounterexample(tt.op, 1, 3000)
			require.Error(s.T(), err)
		})
	}
}

// TestOpClassification: every exported func of the package is in specOps,
// so a new edit fails here until the spec models it.
func (s *EditSpecSuite) TestOpClassification() {
	entries, err := os.ReadDir(".")
	require.NoError(s.T(), err)
	var exported []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		require.NoError(s.T(), err)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.IsExported() {
				exported = append(exported, fn.Name.Name)
			}
		}
	}
	var classified []string
	for name := range specOps() {
		classified = append(classified, name)
	}
	require.ElementsMatch(s.T(), classified, exported, "exported funcs and specOps differ: classify the new one")
}
