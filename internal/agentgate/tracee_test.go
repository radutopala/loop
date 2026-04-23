package agentgate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

type FakeTraceeSuite struct {
	suite.Suite
}

func TestFakeTraceeSuite(t *testing.T) {
	suite.Run(t, new(FakeTraceeSuite))
}

// --- ReadString ---

func (s *FakeTraceeSuite) TestReadStringHit() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x1000: "/bin/ls"}}
	got, err := tr.ReadString(0x1000)
	s.Require().NoError(err)
	s.Require().Equal("/bin/ls", got)
}

func (s *FakeTraceeSuite) TestReadStringMissReturnsGone() {
	tr := &FakeTracee{Strings: map[uintptr]string{}}
	_, err := tr.ReadString(0xBADC0DE)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *FakeTraceeSuite) TestReadStringPreemptedByInjectedError() {
	sentinel := errors.New("boom")
	tr := &FakeTracee{Strings: map[uintptr]string{0x1000: "x"}, StringErr: sentinel}
	_, err := tr.ReadString(0x1000)
	s.Require().ErrorIs(err, sentinel)
}

// --- ReadBytes ---

func (s *FakeTraceeSuite) TestReadBytesHitReturnsCopy() {
	src := []byte{1, 2, 3, 4, 5}
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x1000: src}}
	got, err := tr.ReadBytes(0x1000, 3)
	s.Require().NoError(err)
	s.Require().Equal([]byte{1, 2, 3}, got)

	// Mutating the returned slice must not affect the fake's backing store.
	got[0] = 99
	again, _ := tr.ReadBytes(0x1000, 3)
	s.Require().Equal(byte(1), again[0])
}

func (s *FakeTraceeSuite) TestReadBytesMissReturnsGone() {
	tr := &FakeTracee{Bytes: map[uintptr][]byte{}}
	_, err := tr.ReadBytes(0xBAD, 8)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *FakeTraceeSuite) TestReadBytesTooShortReturnsGone() {
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x1000: {1, 2}}}
	_, err := tr.ReadBytes(0x1000, 8)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

func (s *FakeTraceeSuite) TestReadBytesPreemptedByInjectedError() {
	sentinel := errors.New("bytes-boom")
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x1000: {1, 2, 3}}, BytesErr: sentinel}
	_, err := tr.ReadBytes(0x1000, 3)
	s.Require().ErrorIs(err, sentinel)
}

// --- ReadPointerArray ---

func (s *FakeTraceeSuite) TestReadPointerArrayCopiesList() {
	tr := &FakeTracee{PointerLists: map[uintptr][]string{
		0x2000: {"/bin/git", "push", "origin"},
	}}
	got, err := tr.ReadPointerArray(0x2000, 0)
	s.Require().NoError(err)
	s.Require().Equal([]string{"/bin/git", "push", "origin"}, got)

	// Mutating the returned slice must not affect the fake's backing store.
	got[0] = "mutated"
	again, _ := tr.ReadPointerArray(0x2000, 0)
	s.Require().Equal("/bin/git", again[0])
}

func (s *FakeTraceeSuite) TestReadPointerArrayTruncatesToMax() {
	tr := &FakeTracee{PointerLists: map[uintptr][]string{
		0x2000: {"a", "b", "c", "d"},
	}}
	got, err := tr.ReadPointerArray(0x2000, 2)
	s.Require().NoError(err)
	s.Require().Equal([]string{"a", "b"}, got)
}

func (s *FakeTraceeSuite) TestReadPointerArrayMissReturnsGone() {
	tr := &FakeTracee{PointerLists: map[uintptr][]string{}}
	_, err := tr.ReadPointerArray(0xDEAD, 16)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

// --- ResolveDirfd ---

func (s *FakeTraceeSuite) TestResolveDirfdHit() {
	tr := &FakeTracee{Dirfds: map[int32]string{5: "/work/sub"}}
	got, err := tr.ResolveDirfd(5)
	s.Require().NoError(err)
	s.Require().Equal("/work/sub", got)
}

func (s *FakeTraceeSuite) TestResolveDirfdMissReturnsGone() {
	tr := &FakeTracee{Dirfds: map[int32]string{}}
	_, err := tr.ResolveDirfd(7)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

// --- EvalSymlinks ---

func (s *FakeTraceeSuite) TestEvalSymlinksUnmappedReturnsInputUnchanged() {
	tr := &FakeTracee{}
	got, err := tr.EvalSymlinks("/plain/path")
	s.Require().NoError(err)
	s.Require().Equal("/plain/path", got)
}

func (s *FakeTraceeSuite) TestEvalSymlinksRemapsKnownTargets() {
	tr := &FakeTracee{Symlinks: map[string]string{"/work/link": "/etc/shadow"}}
	got, err := tr.EvalSymlinks("/work/link")
	s.Require().NoError(err)
	s.Require().Equal("/etc/shadow", got)
}

// --- Constants sanity ---

func (s *FakeTraceeSuite) TestConstants() {
	s.Require().Equal(int32(-100), AtFDCWD)
	s.Require().Equal(4096, PATHMAX)
	s.Require().Equal(1024, ArgvMax)
}
