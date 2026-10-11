package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
)

type SettingsSuite struct {
	suite.Suite
}

func TestSettingsSuite(t *testing.T) {
	suite.Run(t, new(SettingsSuite))
}

func (s *SettingsSuite) TestResolveSettings() {
	lo, hi := float64(1), float64(10)
	m := &Manifest{Settings: map[string]*config.SchemaProperty{
		"count": {Type: "integer", Minimum: &lo, Maximum: &hi, Default: float64(5)},
		"lang":  {Type: "string", Enum: []any{"en", "ro"}},
		"code":  {Type: "string", Pattern: "^[a-z]+$"},
		"tags":  {Type: "array", Items: &config.SchemaProperty{Type: "string"}},
		"style": {Type: "object", Properties: map[string]*config.SchemaProperty{"bold": {Type: "boolean"}}, AdditionalProperties: &config.SchemaProperty{Type: "string"}},
	}}
	tests := []struct {
		name         string
		layers       []map[string]any
		want         map[string]any
		wantProblems []string
	}{
		{name: "defaults", want: map[string]any{"count": float64(5)}},
		{
			name: "each layer over the last",
			layers: []map[string]any{
				{"count": float64(3), "lang": "en", "code": "abc"},
				{"count": float64(8), "tags": []any{"a"}, "style": map[string]any{"bold": true, "color": "red"}},
			},
			want: map[string]any{"count": float64(8), "lang": "en", "code": "abc", "tags": []any{"a"}, "style": map[string]any{"bold": true, "color": "red"}},
		},
		{
			name: "values the schema refuses are left out",
			layers: []map[string]any{
				{"count": float64(3)},
				{"count": float64(11), "lang": "fr", "code": "A1", "nope": true, "style": map[string]any{"bold": "yes"}},
			},
			want: map[string]any{"count": float64(3)},
			wantProblems: []string{
				`setting "code": `,
				`setting "count": `,
				`setting "lang": `,
				`setting "nope": the plugin has no such setting`,
				`setting "style": `,
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			got, problems := m.ResolveSettings(tt.layers...)
			require.Equal(s.T(), tt.want, got)
			require.Len(s.T(), problems, len(tt.wantProblems), problems)
			for i, p := range tt.wantProblems {
				require.Contains(s.T(), problems[i], p)
			}
		})
	}
}
