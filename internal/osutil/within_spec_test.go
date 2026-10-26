package osutil

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// Spec: the *In helpers (CheckIn, ReadFileIn, WriteFileIn, MkdirAllIn,
// RenameIn, RemoveIn, OpenRootIn) and RootFS keep every file operation
// inside root. In random trees on a real temp dir, with symlinks of every
// kind (out of root, absolute and relative, to a file, a dir or a missing
// file; to a parent; out and back in; chains; loops; dangling; "." and
// trailing slashes in the target), and for random names (".", "..", "//",
// trailing slashes, empty, relative, outside root), no helper reads,
// creates, changes or removes anything outside root, and a name that stays
// inside root reaches what it names: it isn't refused.
//
// "Stays inside" is decided by refResolve, a reference resolver written
// from os.Root's contract: the name is made relative to root lexically,
// as the helpers do, so "x/link/.." is "x"; its components are then walked
// one at a time from root, following links in place, at most 8 of them. A
// ".." above root leaves it, and so does an absolute link target, even one
// that names a file in root: os.Root refuses absolute links. A link that
// goes out and comes back in leaves it as well.
//
// Trusted base: the real file system (Lstat, Readlink, the walk that
// snapshots everything outside root) and refResolve.

// outMarker starts every file outside root: a read that returns it read
// outside.
const outMarker = "OUT:"

// withinHelpers are the operations under test, one field each, so a spec
// can run against a broken one.
type withinHelpers struct {
	check     func(root, path string) error
	stat      func(root, path string) (os.FileInfo, error)
	readFile  func(root, path string) ([]byte, error)
	writeFile func(root, path string, data []byte) error
	mkdirAll  func(root, path string) error
	remove    func(root, path string) error
	rename    func(root, oldpath, newpath string) error
	openRoot  func(parent, dir string) (*os.Root, error)
}

func realWithinHelpers() withinHelpers {
	return withinHelpers{
		check:     CheckIn,
		stat:      func(root, path string) (os.FileInfo, error) { return RootFS{Root: root}.Stat(path) },
		readFile:  func(root, path string) ([]byte, error) { return RootFS{Root: root}.ReadFile(path) },
		writeFile: func(root, path string, data []byte) error { return RootFS{Root: root}.WriteFile(path, data, 0o644) },
		mkdirAll:  func(root, path string) error { return RootFS{Root: root}.MkdirAll(path, 0o755) },
		remove:    func(root, path string) error { return RootFS{Root: root}.Remove(path) },
		rename:    func(root, oldpath, newpath string) error { return RootFS{Root: root}.Rename(oldpath, newpath) },
		openRoot:  OpenRootIn,
	}
}

type refKind int

const (
	refInside  refKind = iota // resolves to an entry in root
	refMissing                // resolves to a missing entry in an existing dir in root
	refEscape                 // leaves root
	refBroken                 // a missing or non-dir parent, or too many links
	refInvalid                // not a name under root at all
)

func (k refKind) String() string {
	return [...]string{"inside", "missing", "escape", "broken", "invalid"}[k]
}

// refResult is where refResolve finds a name. finalLink tells whether the
// name's last component is a link.
type refResult struct {
	kind      refKind
	path      string
	finalLink bool
}

func (r refResult) String() string { return fmt.Sprintf("%s %s", r.kind, r.path) }

// specWorld is one tree: root, and outside it out and sib, siblings in top.
type specWorld struct {
	top, root, out string
	names          []string
}

// refResolve resolves path as os.Root does, against the real tree. follow
// tells whether a link in the last component is followed.
func (w *specWorld) refResolve(path string, follow bool) refResult {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return refResult{kind: refInvalid}
	}
	parts := strings.Split(rel, "/")
	var stack []string
	links, finalLink := 0, false
	for len(parts) > 0 {
		p := parts[0]
		parts = parts[1:]
		last := len(parts) == 0
		switch p {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return refResult{kind: refEscape}
			}
			stack = stack[:len(stack)-1]
			continue
		}
		abs := filepath.Join(w.root, filepath.Join(stack...), p)
		fi, err := os.Lstat(abs)
		isLink := err == nil && fi.Mode()&fs.ModeSymlink != 0
		switch {
		case err != nil && last:
			return refResult{kind: refMissing, path: abs, finalLink: finalLink}
		case err != nil:
			return refResult{kind: refBroken, path: abs}
		case isLink && (!last || follow):
			finalLink = finalLink || last
			if links++; links > rootMaxSymlinks {
				return refResult{kind: refBroken, path: abs}
			}
			target, _ := os.Readlink(abs)
			if filepath.IsAbs(target) {
				return refResult{kind: refEscape}
			}
			parts = append(strings.Split(target, "/"), parts...)
			continue
		case !last && !fi.IsDir():
			return refResult{kind: refBroken, path: abs}
		}
		finalLink = finalLink || isLink
		stack = append(stack, p)
	}
	return refResult{kind: refInside, path: filepath.Join(w.root, filepath.Join(stack...)), finalLink: finalLink}
}

// rootMaxSymlinks is os.Root's limit on links followed in one name.
const rootMaxSymlinks = 8

// specBytes turns fuzz input into choices; past its end it goes on with a
// generator seeded from it.
type specBytes struct {
	data []byte
	i    int
	x    uint32
}

func newSpecBytes(data []byte) *specBytes {
	g := &specBytes{data: data, x: uint32(len(data))}
	for _, b := range data {
		g.x = g.x*31 + uint32(b)
	}
	return g
}

func (g *specBytes) next(n int) int {
	var b byte
	if g.i < len(g.data) {
		b = g.data[g.i]
	} else {
		g.x = g.x*1103515245 + 12345
		b = byte(g.x >> 16)
	}
	g.i++
	return int(b) % n
}

// specLinkDirs are the dirs in root a link may be planted in.
var specLinkDirs = []string{"", "a", "a/b", "e"}

// specLink is one link to plant: its path under root and its target.
type specLink struct{ path, target string }

// newSpecWorld builds the fixed tree in dir and plants links in root.
func newSpecWorld(t require.TestingT, dir string, links []specLink) *specWorld {
	top, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	w := &specWorld{top: top, root: filepath.Join(top, "root"), out: filepath.Join(top, "out")}
	for _, d := range []string{"root/a/b", "root/e", "out/d"} {
		require.NoError(t, os.MkdirAll(filepath.Join(top, d), 0o755))
	}
	for p, data := range map[string]string{
		"root/f": "in:f", "root/a/g": "in:a/g",
		"out/secret": outMarker + "secret", "out/d/s": outMarker + "d/s", "sib": outMarker + "sib",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(top, p), []byte(data), 0o644))
	}
	require.NoError(t, os.Symlink(w.root, filepath.Join(w.out, "back")))
	require.NoError(t, os.Symlink("../root", filepath.Join(w.out, "relback")))
	for _, l := range links {
		require.NoError(t, os.Symlink(l.target, filepath.Join(w.root, l.path)))
	}
	return w
}

// specTargets are the targets a link at depth d under root may get; i is
// the link's number, chain another link's path under root.
func (w *specWorld) specTargets(d, i int, chain string) []string {
	up := strings.Repeat("../", d)
	return []string{
		"f", "a", "a/g", "a/b", "nope", "a/nope", ".", "..", "f/", "nope/",
		up + "f", up + "a/b", up + "e/", up + "a//./g", up + "a/b/", up + "f/x",
		up + "../out", up + "../out/secret", up + "../out/new", up + "../out/d/",
		up + "../sib", up + "../root/f", up + "../out/back/f", up + "../out/relback/a", up + "..",
		filepath.Join(w.root, "f"), w.root + "/a", w.out, w.out + "/secret", w.out + "/new", w.top,
		"l" + strconv.Itoa(i), up + chain,
	}
}

// specComponents are what random names are made of.
var specComponents = []string{"f", "a", "b", "g", "e", "l0", "l1", "l2", "l3", "l4", "..", ".", "", "nope", "secret", "out"}

// randomSpecWorld builds a world from fuzz input: links planted at random
// with random targets, and random names to drive the helpers with.
func randomSpecWorld(t require.TestingT, dir string, data []byte) *specWorld {
	g := newSpecBytes(data)
	n := 2 + g.next(6)
	paths := make([]string, n)
	dirs := make([]int, n)
	for i := range n {
		dirs[i] = g.next(len(specLinkDirs))
		paths[i] = filepath.Join(specLinkDirs[dirs[i]], "l"+strconv.Itoa(i))
	}
	top, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	shape := &specWorld{top: top, root: filepath.Join(top, "root"), out: filepath.Join(top, "out")}
	links := make([]specLink, n)
	for i := range n {
		d := strings.Count(specLinkDirs[dirs[i]], "/") + 1
		if dirs[i] == 0 {
			d = 0
		}
		chain := "l" + strconv.Itoa(g.next(n+1)) // l<n> is a dangling link name
		if j := g.next(n); j != i {
			chain = paths[j]
		}
		targets := shape.specTargets(d, i, chain)
		links[i] = specLink{paths[i], targets[g.next(len(targets))]}
	}
	w := newSpecWorld(t, dir, links)
	for range 6 + g.next(10) {
		comps := make([]string, 1+g.next(4))
		for i := range comps {
			comps[i] = specComponents[g.next(len(specComponents))]
		}
		if g.next(2) == 0 { // start at a link, half the time
			comps = append([]string{paths[g.next(n)]}, comps[1:]...)
		}
		name := w.root + "/" + strings.Join(comps, "/")
		if g.next(4) == 0 {
			name += "/"
		}
		w.names = append(w.names, name)
	}
	return w
}

// fixedNames are names every world is driven with as well.
func (w *specWorld) fixedNames() []string {
	return []string{
		"", "f", w.root, w.root + "/", w.top, w.out + "/secret", w.root + "/../out/secret",
		w.root + "/../out/new", w.root + "/a/../../sib", w.root + "//a/./g", w.root + "/a/b/../g/",
	}
}

// snapshot records every entry under top outside root: its mode and link
// target, or its mode, size and modification time, which a write, or a
// create or remove in a dir, changes.
func (w *specWorld) snapshot() (map[string]string, error) {
	got := map[string]string{}
	err := filepath.WalkDir(w.top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == w.root {
			return filepath.SkipDir
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		got[p] = fmt.Sprintf("%v %d %d", fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		if fi.Mode()&fs.ModeSymlink != 0 {
			t, err := os.Readlink(p)
			got[p] = "link to " + t
			return err
		}
		return nil
	})
	return got, err
}

// specOp drives one helper with name (and other, for a rename) and checks
// it against the reference.
type specOp func(w *specWorld, h withinHelpers, name, other string) error

// specOps are run in order, the ones that change nothing first.
var specOps = []specOp{
	(*specWorld).checkCheck, (*specWorld).checkStat, (*specWorld).checkRead, (*specWorld).checkOpenRoot,
	(*specWorld).checkWrite, (*specWorld).checkMkdirAll, (*specWorld).checkRename, (*specWorld).checkRemove,
}

// checkHelpers drives h with every name and returns the first violation:
// a change outside root, a read of a file outside it, or an outcome the
// reference doesn't allow.
func (w *specWorld) checkHelpers(h withinHelpers) error {
	names := append(w.fixedNames(), w.names...)
	before, err := w.snapshot()
	if err != nil {
		return err
	}
	for _, op := range specOps {
		for i, name := range names {
			if err := op(w, h, name, names[(i+1)%len(names)]); err != nil {
				return err
			}
			after, err := w.snapshot()
			if err != nil {
				return err
			}
			if !maps.Equal(before, after) {
				return fmt.Errorf("%q changed outside root: %v, was %v", name, after, before)
			}
		}
	}
	return nil
}

func violation(op, name string, ref refResult, err error) error {
	return fmt.Errorf("%s(%q) = %v, the reference resolves it %s", op, name, err, ref)
}

// refused reports whether err is how a helper must fail on a name that
// doesn't resolve inside root: as an escape when it leaves root, with any
// other error when it can't be resolved.
func refused(ref refResult, err error) bool {
	switch ref.kind {
	case refEscape:
		return IsEscape(err)
	case refBroken:
		return err != nil && !IsEscape(err)
	}
	return err != nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func (w *specWorld) checkCheck(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, true)
	err := h.check(w.root, name)
	ok := refused(ref, err)
	switch ref.kind {
	case refInside, refMissing:
		ok = err == nil
	case refBroken:
		ok = !IsEscape(err) // a missing parent is nothing to follow
	}
	if !ok {
		return violation("CheckIn", name, ref, err)
	}
	return nil
}

func (w *specWorld) checkStat(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, true)
	fi, err := h.stat(w.root, name)
	ok := refused(ref, err)
	switch ref.kind {
	case refInside:
		want, werr := os.Stat(ref.path)
		ok = err == nil && werr == nil && os.SameFile(fi, want)
	case refMissing:
		ok = errors.Is(err, fs.ErrNotExist)
	}
	if !ok {
		return violation("Stat", name, ref, err)
	}
	return nil
}

func (w *specWorld) checkRead(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, true)
	data, err := h.readFile(w.root, name)
	if bytes.Contains(data, []byte(outMarker)) {
		return fmt.Errorf("ReadFile(%q) read outside root: %q", name, data)
	}
	ok := refused(ref, err)
	switch {
	case ref.kind == refInside && isDir(ref.path):
		ok = err != nil && !IsEscape(err)
	case ref.kind == refInside:
		want, werr := os.ReadFile(ref.path)
		ok = err == nil && werr == nil && bytes.Equal(data, want)
	case ref.kind == refMissing:
		ok = errors.Is(err, fs.ErrNotExist)
	}
	if !ok {
		return violation("ReadFile", name, ref, err)
	}
	return nil
}

func (w *specWorld) checkOpenRoot(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, false)
	r, err := h.openRoot(w.root, name)
	var opened os.FileInfo
	if err == nil {
		opened, _ = r.Stat(".")
		_ = r.Close()
	}
	ok := refused(ref, err)
	switch {
	case ref.kind == refInside && ref.finalLink:
		// The dir may not be a link, even one that stays in root.
		fol := w.refResolve(name, true)
		wantEscape := fol.kind == refEscape || fol.kind == refInside && isDir(fol.path)
		ok = err != nil && IsEscape(err) == wantEscape
	case ref.kind == refInside && isDir(ref.path):
		want, werr := os.Lstat(ref.path)
		ok = err == nil && werr == nil && os.SameFile(opened, want)
	case ref.kind == refInside:
		ok = err != nil && !IsEscape(err)
	case ref.kind == refMissing:
		ok = errors.Is(err, fs.ErrNotExist)
	}
	if !ok {
		return violation("OpenRootIn", name, ref, err)
	}
	return nil
}

func (w *specWorld) checkWrite(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, true)
	data := []byte("written:" + name)
	err := h.writeFile(w.root, name, data)
	ok := refused(ref, err)
	switch {
	case ref.kind == refInside && isDir(ref.path):
		ok = err != nil && !IsEscape(err)
	case ref.kind == refInside, ref.kind == refMissing:
		got, gerr := os.ReadFile(ref.path)
		ok = err == nil && gerr == nil && bytes.Equal(got, data)
	}
	if !ok {
		return violation("WriteFile", name, ref, err)
	}
	return nil
}

func (w *specWorld) checkMkdirAll(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, true)
	err := h.mkdirAll(w.root, name)
	ok := refused(ref, err)
	switch ref.kind {
	case refInside:
		ok = isDir(ref.path) == (err == nil) && !IsEscape(err)
	case refMissing:
		// MkdirAll doesn't create a dangling link's target.
		ok = !ref.finalLink && err == nil && isDir(ref.path) || ref.finalLink && err != nil && !IsEscape(err)
	case refBroken:
		// A missing parent is created on the way, so it may succeed; what
		// it made must then be a dir in root.
		now := w.refResolve(name, true)
		ok = err != nil || now.kind == refInside && isDir(now.path)
	}
	if !ok {
		return violation("MkdirAll", name, ref, err)
	}
	return nil
}

func (w *specWorld) checkRename(h withinHelpers, name, other string) error {
	from, to := w.refResolve(name, false), w.refResolve(other, false)
	moved, _ := os.Lstat(from.path)
	var target os.FileInfo
	if to.kind == refInside {
		target, _ = os.Lstat(to.path)
	}
	err := h.rename(w.root, name, other)
	ok := true
	switch {
	case from.kind != refInside || to.kind == refEscape || to.kind == refInvalid || to.kind == refBroken:
		ok = err != nil
	case from.path != w.root && moved != nil && !moved.IsDir() && to.path != w.root &&
		(to.kind == refMissing || target != nil && !target.IsDir()):
		// A file or a link onto a free name, a file or a link.
		got, gerr := os.Lstat(to.path)
		ok = err == nil && gerr == nil && os.SameFile(got, moved)
	}
	if !ok {
		return fmt.Errorf("Rename(%q, %q) = %v, the reference resolves them %s and %s", name, other, err, from, to)
	}
	return nil
}

func (w *specWorld) checkRemove(h withinHelpers, name, _ string) error {
	ref := w.refResolve(name, false)
	var entries []os.DirEntry
	if ref.kind == refInside && !ref.finalLink {
		entries, _ = os.ReadDir(ref.path)
	}
	err := h.remove(w.root, name)
	ok := refused(ref, err)
	switch {
	case ref.kind == refInside && (ref.path == w.root || len(entries) > 0):
		ok = err != nil && !IsEscape(err)
	case ref.kind == refInside:
		_, lerr := os.Lstat(ref.path)
		ok = err == nil && errors.Is(lerr, fs.ErrNotExist)
	case ref.kind == refMissing:
		ok = errors.Is(err, fs.ErrNotExist)
	}
	if !ok {
		return violation("Remove", name, ref, err)
	}
	return nil
}

type WithinSpecSuite struct {
	suite.Suite
}

func TestWithinSpecSuite(t *testing.T) {
	suite.Run(t, new(WithinSpecSuite))
}

// specSeeds are the inputs of the random worlds the suite sweeps.
func specSeeds(n int) [][]byte {
	rng := rand.New(rand.NewPCG(1, 2))
	seeds := make([][]byte, n)
	for i := range seeds {
		seeds[i] = make([]byte, 48)
		for j := range seeds[i] {
			seeds[i][j] = byte(rng.Uint32())
		}
	}
	return seeds
}

// TestExamples pins the reference on one name per case, and checks the
// helpers on that world.
func (s *WithinSpecSuite) TestExamples() {
	chain := func(n int) []specLink {
		links := make([]specLink, n)
		for i := range n - 1 {
			links[i] = specLink{"l" + strconv.Itoa(i), "l" + strconv.Itoa(i+1)}
		}
		links[n-1] = specLink{"l" + strconv.Itoa(n-1), "f"}
		return links
	}
	for _, tc := range []struct {
		name  string
		links []specLink
		path  string // {root} and {out} are replaced, here and in targets
		want  refKind
	}{
		{"plain file", nil, "{root}/a/g", refInside},
		{"relative link in root", []specLink{{"a/l0", "../f"}}, "{root}/a/l0", refInside},
		{"name cleaned before links", []specLink{{"l0", "a//./b/"}}, "{root}/l0/../g", refMissing},
		{"link to root itself", []specLink{{"a/l0", ".."}}, "{root}/a/l0/f", refInside},
		{"dangling link in root", []specLink{{"l0", "a/nope"}}, "{root}/l0", refMissing},
		{"absolute link in root", []specLink{{"l0", "{root}/f"}}, "{root}/l0", refEscape},
		{"absolute link out", []specLink{{"l0", "{out}/secret"}}, "{root}/l0", refEscape},
		{"relative link out", []specLink{{"a/b/l0", "../../../out/secret"}}, "{root}/a/b/l0", refEscape},
		{"dangling link out", []specLink{{"l0", "../out/new"}}, "{root}/l0", refEscape},
		{"dir link out", []specLink{{"e/l0", "../../out/d"}}, "{root}/e/l0/s", refEscape},
		{"link to the parent of root", []specLink{{"l0", ".."}}, "{root}/l0/sib", refEscape},
		{"out and back in", []specLink{{"l0", "../out/relback/f"}}, "{root}/l0", refEscape},
		{"chain of 8", chain(8), "{root}/l0", refInside},
		{"chain of 9", chain(9), "{root}/l0", refBroken},
		{"loop", []specLink{{"l0", "l1"}, {"l1", "./l0"}}, "{root}/l0", refBroken},
		{"self loop", []specLink{{"l0", "l0"}}, "{root}/l0/x", refBroken},
		{"trailing slash on a file", []specLink{{"l0", "f/"}}, "{root}/l0", refBroken},
		{"through a file", nil, "{root}/f/x", refBroken},
		{"dot-dot out of root", nil, "{root}/a/../../out/secret", refEscape},
		{"empty", nil, "", refInvalid},
		{"relative", nil, "f", refInvalid},
	} {
		s.Run(tc.name, func() {
			dir := s.T().TempDir()
			top, err := filepath.EvalSymlinks(dir)
			s.Require().NoError(err)
			r := strings.NewReplacer("{root}", filepath.Join(top, "root"), "{out}", filepath.Join(top, "out"))
			links := make([]specLink, len(tc.links))
			for i, l := range tc.links {
				links[i] = specLink{l.path, r.Replace(l.target)}
			}
			w := newSpecWorld(s.T(), dir, links)
			path := r.Replace(tc.path)
			w.names = []string{path}
			s.Require().Equal(tc.want, w.refResolve(path, true).kind)
			s.Require().NoError(w.checkHelpers(realWithinHelpers()))
		})
	}
}

func (s *WithinSpecSuite) TestRandomWorlds() {
	for i, seed := range specSeeds(32) {
		w := randomSpecWorld(s.T(), s.T().TempDir(), seed)
		s.Require().NoError(w.checkHelpers(realWithinHelpers()), "seed %d: %x", i, seed)
	}
}

// The spec must be able to fail: each broken helper below gives a
// counterexample in the worlds the suite sweeps. They only read, so a
// broken one can't touch anything outside the test's dirs.
func (s *WithinSpecSuite) TestSpecCanFail() {
	lexicalEscape := func(root, path string) error {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return &os.PathError{Op: "open", Path: rel, Err: errEscapes}
		}
		return nil
	}
	for name, broken := range map[string]func(*withinHelpers){
		"read checked by spelling only": func(h *withinHelpers) {
			h.readFile = func(root, path string) ([]byte, error) {
				if err := lexicalEscape(root, path); err != nil {
					return nil, err
				}
				return os.ReadFile(path)
			}
		},
		"check by spelling only": func(h *withinHelpers) { h.check = lexicalEscape },
		"stat refuses every link": func(h *withinHelpers) {
			h.stat = func(root, path string) (os.FileInfo, error) {
				if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
					return nil, &os.PathError{Op: "stat", Path: path, Err: errEscapes}
				}
				return RootFS{Root: root}.Stat(path)
			}
		},
	} {
		s.Run(name, func() {
			h := realWithinHelpers()
			broken(&h)
			var found error
			for _, seed := range specSeeds(32) {
				if found = randomSpecWorld(s.T(), s.T().TempDir(), seed).checkHelpers(h); found != nil {
					break
				}
			}
			s.Require().Error(found)
		})
	}
}

// FuzzHelpersStayInRoot checks the property on random worlds. Seeds are in
// testdata/fuzz/FuzzHelpersStayInRoot.
func FuzzHelpersStayInRoot(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		w := randomSpecWorld(t, t.TempDir(), data)
		require.NoError(t, w.checkHelpers(realWithinHelpers()))
	})
}
