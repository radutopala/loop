package readme

import (
	"encoding/base64"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EmbedSuite struct {
	suite.Suite
}

func TestEmbedSuite(t *testing.T) {
	suite.Run(t, new(EmbedSuite))
}

func (s *EmbedSuite) TestContentNotEmpty() {
	require.NotEmpty(s.T(), Content)
}

func (s *EmbedSuite) TestContentIsMarkdown() {
	require.Contains(s.T(), Content, "# ")
}

func (s *EmbedSuite) TestInline() {
	files := fstest.MapFS{"img/a.svg": {Data: []byte("<svg/>")}}
	tests := []struct {
		name string
		md   string
		want string
	}{
		{"shipped image", `<img src="img/a.svg" alt="a">`, `<img src="data:image/svg+xml;base64,` + base64.StdEncoding.EncodeToString([]byte("<svg/>")) + `" alt="a">`},
		{"missing image", `<img src="img/b.svg">`, `<img src="img/b.svg">`},
		{"absolute url", `<img src="https://example.com/a.svg">`, `<img src="https://example.com/a.svg">`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, inline(tt.md, files))
		})
	}
}

func (s *EmbedSuite) TestInlinedShipsArchitecture() {
	require.Contains(s.T(), Inlined(), `src="data:image/svg+xml;base64,`)
	require.NotContains(s.T(), Inlined(), `src="docs/static/images/architecture.svg"`)
}
