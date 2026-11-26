package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TagsSuite struct {
	suite.Suite
}

func TestTagsSuite(t *testing.T) {
	suite.Run(t, new(TagsSuite))
}

func (s *TagsSuite) TestNormalizeTag() {
	tests := []struct {
		name string
		raw  string
		want string
		err  string
	}{
		{name: "plain", raw: "bug", want: "bug"},
		{name: "trims, lowercases and drops a hash", raw: "  #Front-End ", want: "front-end"},
		{name: "keeps dots, slashes and underscores", raw: "team/web_v1.2", want: "team/web_v1.2"},
		{name: "letters beyond ascii", raw: "Ünïcode", want: "ünïcode"},
		{name: "empty", raw: "  ", err: "can't be empty"},
		{name: "only a hash", raw: "#", err: "can't be empty"},
		{name: "too long", raw: strings.Repeat("a", MaxTagLen+1), err: "longer than 32"},
		{name: "space", raw: "two words", err: "may hold only"},
		{name: "colon", raw: "tag:x", err: "may hold only"},
		{name: "starts with a dash", raw: "-x", err: "may hold only"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, err := NormalizeTag(tt.raw)
			if tt.err != "" {
				require.ErrorContains(s.T(), err, tt.err)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *TagsSuite) TestNormalizeTags() {
	got, err := NormalizeTags([]string{"Web", "api", "#web"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"api", "web"}, got)

	got, err = NormalizeTags(nil)
	require.NoError(s.T(), err)
	require.Empty(s.T(), got)

	_, err = NormalizeTags([]string{"ok", "not ok"})
	require.ErrorContains(s.T(), err, "may hold only")
}
