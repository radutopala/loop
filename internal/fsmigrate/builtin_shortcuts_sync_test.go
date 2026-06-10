package fsmigrate

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"
	"github.com/tailscale/hujson"
)

// TestBuiltinShortcutsMatchExampleConfig guards against drift between the
// shortcut prompts seeded by fsmigrate (the upgrade path) and the entries in
// config.global.example.json (the fresh-install path). The two are duplicated
// by design — the example file is user-facing HJSON, the consts feed the
// hujson seeders — and were previously kept in sync by comment only.
func (s *FSMigrateSuite) TestBuiltinShortcutsMatchExampleConfig() {
	raw, err := os.ReadFile(filepath.Join("..", "config", "config.global.example.json"))
	require.NoError(s.T(), err)

	std, err := hujson.Standardize(raw)
	require.NoError(s.T(), err)

	var cfg struct {
		PromptShortcuts []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		} `json:"prompt_shortcuts"`
	}
	require.NoError(s.T(), json.Unmarshal(std, &cfg))

	byName := map[string]struct{ description, prompt string }{}
	for _, sc := range cfg.PromptShortcuts {
		byName[sc.Name] = struct{ description, prompt string }{sc.Description, sc.Prompt}
	}

	tests := []struct {
		name        string
		description string
		prompt      string
	}{
		{builtinCodeReviewShortcutName, builtinCodeReviewShortcutDescription, builtinCodeReviewShortcutPrompt},
		{builtinSimplifyShortcutName, builtinSimplifyShortcutDescription, builtinSimplifyShortcutPrompt},
	}
	for _, tt := range tests {
		entry, ok := byName[tt.name]
		require.True(s.T(), ok, "example config is missing the %q shortcut", tt.name)
		require.Equal(s.T(), tt.description, entry.description, "%q description drifted from the fsmigrate const", tt.name)
		require.Equal(s.T(), tt.prompt, entry.prompt, "%q prompt drifted from the fsmigrate const", tt.name)
	}
}
