package learn

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MemorySuite struct {
	suite.Suite
}

func TestMemorySuite(t *testing.T) {
	suite.Run(t, new(MemorySuite))
}

func (s *MemorySuite) TestFile() {
	m := &Memory{Name: "lint-first", Description: " Lint before pushing ", Type: "feedback", Body: "\nRun make lint.\n\n**Why:** CI.\n"}
	require.Equal(s.T(), "lint-first.md", m.File())
	require.Equal(s.T(), "---\nname: lint-first\ndescription: Lint before pushing\nmetadata:\n  type: feedback\n---\n\nRun make lint.\n\n**Why:** CI.\n", m.Content())
	require.Equal(s.T(), "- [lint-first](lint-first.md) — Lint before pushing", m.IndexLine())
}

// A slug YAML reads as another type is quoted in the frontmatter.
func (s *MemorySuite) TestContentQuotesName() {
	m := &Memory{Name: "true", Description: "123", Type: "user", Body: "b"}
	require.Equal(s.T(), "---\nname: \"true\"\ndescription: \"123\"\nmetadata:\n  type: user\n---\n\nb\n", m.Content())
}

func (s *MemorySuite) TestIndexed() {
	m := &Memory{Name: "lint-first"}
	tests := []struct {
		name  string
		index string
		want  bool
	}{
		{"empty", "", false},
		{"pointed at", "# Memory\n- [Lint](lint-first.md) — x\n", true},
		{"another memory", "- [Lint](lint-first-too.md) — x\n", false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, m.Indexed(tc.index))
		})
	}
}

// TestYAMLScalar quotes what YAML wouldn't read back as the same plain
// string.
func (s *MemorySuite) TestYAMLScalar() {
	tests := []struct {
		in, want string
	}{
		{"Lint before pushing", "Lint before pushing"},
		{"Use x:y ports", "Use x:y ports"},
		{"", `""`},
		{"Note: lint first", `"Note: lint first"`},
		{"ends with colon:", `"ends with colon:"`},
		{"has a # comment", `"has a # comment"`},
		{"- a list?", `"- a list?"`},
		{`"quoted" start`, `"\"quoted\" start"`},
		{"[bracket]", `"[bracket]"`},
		{"true", `"true"`},
		{"null", `"null"`},
		{"~", `"~"`},
		{"123", `"123"`},
		{"1e3", `"1e3"`},
		{"tab\t# comment", `"tab\t# comment"`},
		{"a\nb", `"a\nb"`},
		{"x: y: z", `"x: y: z"`},
	}
	for _, tc := range tests {
		s.Run(tc.in, func() {
			require.Equal(s.T(), tc.want, yamlScalar(tc.in))
		})
	}
}
