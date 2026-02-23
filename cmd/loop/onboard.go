package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/config"
	containerimage "github.com/radutopala/loop/internal/container/image"
)

func newOnboardGlobalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "onboard:global",
		Aliases: []string{"o:global", "setup"},
		Short:   "Initialize global Loop configuration at ~/.loop/",
		Long:    "Copies config.example.json to ~/.loop/config.json for first-time setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			ownerID, _ := cmd.Flags().GetString("owner-id")
			return onboardGlobal(force, ownerID)
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite existing config")
	cmd.Flags().String("owner-id", "", "Set RBAC owner user ID (exits bootstrap mode)")
	return cmd
}

func newOnboardLocalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "onboard:local",
		Aliases: []string{"o:local", "init"},
		Short:   "Register Loop MCP server in the current project",
		Long:    "Writes .mcp.json with the loop MCP server for Claude Code integration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			apiURL, _ := cmd.Flags().GetString("api-url")
			ownerID, _ := cmd.Flags().GetString("owner-id")
			return onboardLocal(apiURL, ownerID)
		},
	}
	cmd.Flags().String("api-url", "http://localhost:8222", "Loop API base URL")
	cmd.Flags().String("owner-id", "", "Set RBAC owner user ID in project config")
	return cmd
}

func onboardGlobal(force bool, ownerID string) error {
	home, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	loopDir := filepath.Join(home, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	// Check if config already exists
	if _, err := osStat(configPath); err == nil {
		if !force {
			return fmt.Errorf("config already exists at %s (use --force to overwrite)", configPath)
		}
	}

	// Create ~/.loop directory if it doesn't exist
	if err := osMkdirAll(loopDir, 0755); err != nil {
		return fmt.Errorf("creating loop directory: %w", err)
	}

	// Prepare config content — optionally inject owner permissions
	configData := config.ExampleConfig
	if ownerID != "" {
		commented := []byte(`  // RBAC permissions: owners can do everything (including allow/deny); members can trigger and manage tasks.
  // If all config and DB permissions are empty, everyone is treated as owner (bootstrap mode).
  //"permissions": {
  //  "owners":  { "users": ["U12345678"], "roles": ["1234567890123456789"] },
  //  "members": { "users": [], "roles": [] }
  //},`)
		uncommented := []byte(fmt.Sprintf(`  "permissions": {
    "owners":  { "users": ["%s"], "roles": [] },
    "members": { "users": [], "roles": [] }
  },`, ownerID))
		configData = bytes.Replace(configData, commented, uncommented, 1)
	}

	// Write embedded example config
	if err := osWriteFile(configPath, configData, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	// Create default .bashrc for container shell aliases
	bashrcPath := filepath.Join(loopDir, ".bashrc")
	if _, err := osStat(bashrcPath); err != nil {
		bashrcContent := []byte("# Shell aliases and config sourced inside Loop containers.\n# Add your aliases here — this file is bind-mounted as ~/.bashrc.\n")
		if err := osWriteFile(bashrcPath, bashrcContent, 0644); err != nil {
			return fmt.Errorf("writing .bashrc: %w", err)
		}
	}

	// Flush embedded container files
	containerDir := filepath.Join(loopDir, "container")
	if err := osMkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("creating container directory: %w", err)
	}
	if err := osWriteFile(filepath.Join(containerDir, "Dockerfile"), containerimage.Dockerfile, 0644); err != nil {
		return fmt.Errorf("writing container Dockerfile: %w", err)
	}
	if err := osWriteFile(filepath.Join(containerDir, "entrypoint.sh"), containerimage.Entrypoint, 0644); err != nil {
		return fmt.Errorf("writing container entrypoint: %w", err)
	}
	setupPath := filepath.Join(containerDir, "setup.sh")
	if _, err := osStat(setupPath); err != nil {
		if err := osWriteFile(setupPath, containerimage.Setup, 0644); err != nil {
			return fmt.Errorf("writing container setup script: %w", err)
		}
	}

	// Write Slack app manifest
	if err := osWriteFile(filepath.Join(loopDir, "slack-manifest.json"), config.SlackManifest, 0644); err != nil {
		return fmt.Errorf("writing Slack manifest: %w", err)
	}

	// Dump embedded templates directory
	templatesDir := filepath.Join(loopDir, "templates")
	if err := osMkdirAll(templatesDir, 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}
	if err := dumpTemplates(templatesDir); err != nil {
		return err
	}

	fmt.Printf("✓ Created config at %s\n", configPath)
	fmt.Println("\nNext steps:")
	fmt.Println("1. Edit config.json and add your platform credentials (Discord or Slack)")
	fmt.Println("   For Slack: create an app from ~/.loop/slack-manifest.json (see README)")
	fmt.Println("2. Run 'loop serve' to start the bot")
	fmt.Println("3. Customize the Dockerfile at ~/.loop/container/ if needed")

	return nil
}

// dumpTemplates writes all embedded template files to the target directory,
// skipping files that already exist (so user edits are preserved).
func dumpTemplates(dir string) error {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return fmt.Errorf("reading embedded templates: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := osStat(dst); err == nil {
			continue // don't overwrite existing
		}
		data, err := templatesFS.ReadFile("templates/" + e.Name())
		if err != nil {
			return fmt.Errorf("reading embedded template %s: %w", e.Name(), err)
		}
		if err := osWriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("writing template %s: %w", e.Name(), err)
		}
	}
	return nil
}

func onboardLocal(apiURL string, ownerID string) error {
	dir, err := osGetwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	mcpPath := filepath.Join(dir, ".mcp.json")

	// Read existing .mcp.json if it exists, merge into it
	existing := make(map[string]any)
	if data, err := osReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing .mcp.json: %w", err)
		}
	}

	// Ensure mcpServers key exists
	servers, _ := existing["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}

	// Build loop server entry (always rebuild to pick up config changes).
	_, alreadyRegistered := servers["loop"]
	args := []string{"mcp", "--dir", dir, "--api-url", apiURL, "--log", filepath.Join(dir, ".loop", "mcp.log")}
	if cfg, err := configLoad(); err == nil && cfg.Memory.Enabled {
		args = append(args, "--memory")
	}
	servers["loop"] = map[string]any{
		"command": "loop",
		"args":    args,
	}
	existing["mcpServers"] = servers

	mcpJSON, _ := json.MarshalIndent(existing, "", "  ")
	if err := osWriteFile(mcpPath, append(mcpJSON, '\n'), 0644); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	if alreadyRegistered {
		fmt.Printf("Updated loop MCP server in %s\n", mcpPath)
	} else {
		fmt.Printf("Added loop MCP server to %s\n", mcpPath)
	}
	fmt.Println("\nMake sure 'loop serve' or 'loop daemon:start' is running.")

	// Write project config example if .loop/config.json doesn't exist
	loopDir := filepath.Join(dir, ".loop")
	projectConfigPath := filepath.Join(loopDir, "config.json")
	if _, err := osStat(projectConfigPath); os.IsNotExist(err) {
		if err := osMkdirAll(loopDir, 0755); err != nil {
			return fmt.Errorf("creating .loop directory: %w", err)
		}
		projectData := config.ProjectExampleConfig
		if ownerID != "" {
			commented := []byte(`  // Permissions override for this project (replaces global permissions when set)
  //"permissions": {
  //  "owners":  { "users": [], "roles": [] },
  //  "members": { "users": [], "roles": [] }
  //},`)
			uncommented := []byte(fmt.Sprintf(`  "permissions": {
    "owners":  { "users": ["%s"], "roles": [] },
    "members": { "users": [], "roles": [] }
  },`, ownerID))
			projectData = bytes.Replace(projectData, commented, uncommented, 1)
		}
		if err := osWriteFile(projectConfigPath, projectData, 0644); err != nil {
			return fmt.Errorf("writing project config: %w", err)
		}
		fmt.Printf("Created project config at %s\n", projectConfigPath)
	}

	// Create templates directory for project-level prompt_path templates
	templatesDir := filepath.Join(loopDir, "templates")
	if err := osMkdirAll(templatesDir, 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}

	// Eagerly create the channel so it's ready immediately
	channelID, err := ensureChannelFunc(apiURL, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not register channel (is 'loop serve' running?): %v\n", err)
	} else {
		fmt.Printf("Channel ready: %s\n", channelID)
	}

	return nil
}
