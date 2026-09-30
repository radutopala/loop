package unidiff

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type UnidiffSuite struct {
	suite.Suite
}

func TestUnidiffSuite(t *testing.T) {
	suite.Run(t, new(UnidiffSuite))
}

func (s *UnidiffSuite) TestLines() {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"final newline", "a\nb\n", []string{"a\n", "b\n"}},
		{"no final newline", "a\nb", []string{"a\n", "b\n"}},
		{"blank line", "\n", []string{"\n"}},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, lines(c.in))
		})
	}
}

func (s *UnidiffSuite) TestDiff() {
	cases := []struct {
		name string
		from string
		a, b string
		want string
	}{
		{"equal", "f", "x\n", "x\n", ""},
		{"new file", "/dev/null", "", "a\nb\n", "--- /dev/null\n+++ g\n@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"change", "f", "a\nb\n", "a\nc\n", "--- f\n+++ g\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n"},
		{"emptied", "f", "a\n", "", "--- f\n+++ g\n@@ -1 +0,0 @@\n-a\n"},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.Require().Equal(c.want, Diff(c.from, "g", c.a, c.b))
		})
	}
}
